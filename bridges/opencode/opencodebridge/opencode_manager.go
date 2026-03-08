package opencodebridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/bridges/opencode/opencode"
	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

// OpenCodeManager coordinates connections to OpenCode server instances,
// dispatches SSE events, and manages session lifecycle.
type OpenCodeManager struct {
	bridge    *Bridge
	mu        sync.RWMutex
	instances map[string]*openCodeInstance
	approvals *bridgeadapter.ApprovalManager[permissionDecision]
}

type permissionApprovalRef struct {
	RoomID       id.RoomID
	InstanceID   string
	SessionID    string
	MessageID    string
	ToolCallID   string
	PermissionID string
}

type permissionDecision struct {
	Response  string
	Reason    string
	DecidedAt time.Time
	DecidedBy id.UserID
}

func NewOpenCodeManager(bridge *Bridge) *OpenCodeManager {
	return &OpenCodeManager{
		bridge:    bridge,
		instances: make(map[string]*openCodeInstance),
		approvals: bridgeadapter.NewApprovalManager[permissionDecision](),
	}
}

func (m *OpenCodeManager) log() *zerolog.Logger {
	if m == nil || m.bridge == nil || m.bridge.host == nil {
		logger := zerolog.Nop()
		return &logger
	}
	base := m.bridge.host.Log()
	if base == nil {
		logger := zerolog.Nop()
		return &logger
	}
	logger := base.With().Str("component", "opencode").Logger()
	return &logger
}

func (m *OpenCodeManager) getInstance(instanceID string) *openCodeInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.instances[instanceID]
}

func (m *OpenCodeManager) IsConnected(instanceID string) bool {
	inst := m.getInstance(instanceID)
	return inst != nil && inst.connected
}

// DisconnectAll stops all in-memory OpenCode connections/event loops without
// modifying persisted instance metadata.
func (m *OpenCodeManager) DisconnectAll() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inst := range m.instances {
		if inst == nil {
			continue
		}
		inst.cancelAndStopTimer()
	}
	m.instances = make(map[string]*openCodeInstance)
}

func (m *OpenCodeManager) RestoreConnections(ctx context.Context) error {
	if m == nil || m.bridge == nil || m.bridge.host == nil {
		return nil
	}
	for _, cfg := range m.bridge.host.OpenCodeInstances() {
		if cfg == nil || strings.TrimSpace(cfg.URL) == "" {
			continue
		}
		password := strings.TrimSpace(cfg.Password)
		if cfg.HasPassword && password == "" {
			m.log().Warn().
				Str("instance", cfg.ID).
				Msg("Skipping OpenCode restore because the password is missing from persisted metadata")
			continue
		}
		if _, _, err := m.Connect(ctx, cfg.URL, password, cfg.Username); err != nil {
			m.log().Warn().Err(err).Str("instance", cfg.ID).Msg("Failed to restore OpenCode instance")
		}
	}
	return nil
}

func (m *OpenCodeManager) Connect(ctx context.Context, baseURL, password, username string) (*openCodeInstance, int, error) {
	if m == nil || m.bridge == nil || m.bridge.host == nil {
		return nil, 0, errors.New("opencode manager unavailable")
	}
	if strings.TrimSpace(baseURL) == "" {
		return nil, 0, errors.New("url is required")
	}
	user := strings.TrimSpace(username)
	if user == "" {
		user = "opencode"
	}

	normalized, err := opencode.NormalizeBaseURL(baseURL)
	if err != nil {
		return nil, 0, fmt.Errorf("normalize url: %w", err)
	}
	instanceID := OpenCodeInstanceID(normalized, user)

	client, err := opencode.NewClient(normalized, user, password)
	if err != nil {
		return nil, 0, fmt.Errorf("create client: %w", err)
	}
	sessions, err := client.ListSessions(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("list sessions: %w", err)
	}

	inst := &openCodeInstance{
		cfg: OpenCodeInstance{
			ID:          instanceID,
			URL:         normalized,
			Username:    user,
			Password:    strings.TrimSpace(password),
			HasPassword: strings.TrimSpace(password) != "",
		},
		password:       strings.TrimSpace(password),
		client:         client,
		connected:      true,
		seenMsg:        make(map[string]map[string]string),
		seenPart:       make(map[string]map[string]*openCodePartState),
		partsByMessage: make(map[string]map[string]map[string]struct{}),
		turnState:      make(map[string]map[string]*openCodeTurnState),
		sendQueue:      make(map[string]*openCodeSessionQueue),
	}

	m.mu.Lock()
	if existing := m.instances[instanceID]; existing != nil {
		existing.cancelAndStopTimer()
	}
	m.instances[instanceID] = inst
	m.mu.Unlock()

	m.persistInstance(ctx, inst)
	m.bridge.EnsureGhostDisplayName(ctx, instanceID)

	count, syncErr := m.syncSessions(ctx, inst, sessions)
	m.startEventLoop(inst)
	return inst, count, syncErr
}

func (m *OpenCodeManager) persistInstance(ctx context.Context, inst *openCodeInstance) {
	meta := m.bridge.host.OpenCodeInstances()
	if meta == nil {
		meta = make(map[string]*OpenCodeInstance)
	}
	meta[inst.cfg.ID] = &OpenCodeInstance{
		ID:          inst.cfg.ID,
		URL:         inst.cfg.URL,
		Username:    inst.cfg.Username,
		Password:    strings.TrimSpace(inst.password),
		HasPassword: inst.cfg.HasPassword,
	}
	if err := m.bridge.host.SaveOpenCodeInstances(ctx, meta); err != nil {
		m.log().Warn().Err(err).Msg("Failed to persist OpenCode instance")
	}
}

func (m *OpenCodeManager) RemoveInstance(ctx context.Context, instanceID string) error {
	if m == nil || m.bridge == nil || m.bridge.host == nil {
		return errors.New("opencode manager unavailable")
	}
	id := strings.TrimSpace(instanceID)
	if id == "" {
		return errors.New("instance id is required")
	}

	m.mu.RLock()
	inst := m.instances[id]
	m.mu.RUnlock()

	if inst != nil {
		m.cleanupInstancePortals(ctx, inst)
	}

	hadInstance := false
	m.mu.Lock()
	if inst := m.instances[id]; inst != nil {
		hadInstance = true
		inst.cancelAndStopTimer()
		delete(m.instances, id)
	}
	m.mu.Unlock()

	meta := m.bridge.host.OpenCodeInstances()
	if meta != nil {
		if _, ok := meta[id]; ok {
			hadInstance = true
		}
		delete(meta, id)
		if len(meta) == 0 {
			meta = nil
		}
	}
	if !hadInstance {
		return ErrInstanceNotFound
	}
	return m.bridge.host.SaveOpenCodeInstances(ctx, meta)
}

func (m *OpenCodeManager) cleanupInstancePortals(ctx context.Context, inst *openCodeInstance) {
	portals, err := m.bridge.listAllChatPortals(ctx)
	if err != nil {
		m.log().Warn().Err(err).Msg("Failed to list portals for cleanup")
		return
	}
	for _, portal := range portals {
		meta := m.bridge.portalMeta(portal)
		if meta == nil || !meta.IsOpenCodeRoom || meta.InstanceID != inst.cfg.ID {
			continue
		}
		if err := inst.client.DeleteSession(ctx, meta.SessionID); err != nil {
			m.log().Warn().Err(err).Str("session", meta.SessionID).Msg("Failed to delete OpenCode session during cleanup")
		}
		m.bridge.host.CleanupPortal(ctx, portal, "opencode instance removed")
	}
}

func (m *OpenCodeManager) requireConnectedInstance(instanceID string) (*openCodeInstance, error) {
	inst := m.getInstance(instanceID)
	if inst == nil {
		return nil, errors.New("unknown OpenCode instance")
	}
	if !inst.connected {
		return nil, errors.New("OpenCode instance disconnected")
	}
	return inst, nil
}

func (m *OpenCodeManager) SendMessage(ctx context.Context, instanceID, sessionID string, parts []opencode.PartInput, eventID id.EventID) error {
	inst, err := m.requireConnectedInstance(instanceID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("session id is required")
	}
	if len(parts) == 0 {
		return errors.New("message parts are required")
	}

	msgID := opencodeMessageIDForEvent(eventID)
	if msgID != "" {
		if inst.isSeen(sessionID, msgID) {
			return nil
		}
	}
	item := &queuedUserMessage{
		sessionID: sessionID,
		eventID:   eventID,
		parts:     parts,
	}
	toSend := inst.enqueueMessage(sessionID, item)
	if toSend == nil {
		return nil
	}
	return m.sendQueuedMessage(ctx, inst, toSend)
}

func (m *OpenCodeManager) sendQueuedMessage(ctx context.Context, inst *openCodeInstance, item *queuedUserMessage) error {
	if inst == nil || item == nil {
		return nil
	}
	msgID := opencodeMessageIDForEvent(item.eventID)
	if err := inst.client.SendMessageAsync(ctx, item.sessionID, msgID, item.parts); err != nil {
		inst.requeueMessageFront(item.sessionID, item)
		inst.releaseActiveSession(item.sessionID)
		if opencode.IsAuthError(err) {
			m.setConnected(inst, false)
		}
		return fmt.Errorf("send message: %w", err)
	}
	if msgID != "" {
		inst.markSeen(item.sessionID, msgID, "user")
	}
	return nil
}

func (m *OpenCodeManager) processNextQueued(ctx context.Context, inst *openCodeInstance, sessionID string) {
	if inst == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	next := inst.markSessionIdle(sessionID)
	if next == nil {
		return
	}
	if err := m.sendQueuedMessage(ctx, inst, next); err != nil {
		m.log().Warn().Err(err).
			Str("instance", inst.cfg.ID).
			Str("session", sessionID).
			Msg("Failed to send queued OpenCode message")
		portal := m.bridge.findOpenCodePortal(ctx, inst.cfg.ID, sessionID)
		if portal != nil {
			m.bridge.host.SendSystemNotice(ctx, portal, "OpenCode send failed: "+err.Error())
		}
	}
}

func (m *OpenCodeManager) DeleteSession(ctx context.Context, instanceID, sessionID string) error {
	inst := m.getInstance(instanceID)
	if inst == nil {
		return errors.New("unknown OpenCode instance")
	}
	return inst.client.DeleteSession(ctx, sessionID)
}

func (m *OpenCodeManager) AbortSession(ctx context.Context, instanceID, sessionID string) error {
	inst, err := m.requireConnectedInstance(instanceID)
	if err != nil {
		return err
	}
	if err := inst.client.AbortSession(ctx, sessionID); err != nil {
		if opencode.IsAuthError(err) {
			m.setConnected(inst, false)
		}
		return fmt.Errorf("abort session: %w", err)
	}
	return nil
}

func (m *OpenCodeManager) runSessionMutation(
	ctx context.Context,
	instanceID string,
	action string,
	run func(*openCodeInstance) (*opencode.Session, error),
) (*opencode.Session, error) {
	inst, err := m.requireConnectedInstance(instanceID)
	if err != nil {
		return nil, err
	}
	session, err := run(inst)
	if err != nil {
		if opencode.IsAuthError(err) {
			m.setConnected(inst, false)
		}
		return nil, fmt.Errorf("%s: %w", action, err)
	}
	return session, nil
}

func (m *OpenCodeManager) CreateSession(ctx context.Context, instanceID, title, directory string) (*opencode.Session, error) {
	return m.runSessionMutation(ctx, instanceID, "create session", func(inst *openCodeInstance) (*opencode.Session, error) {
		return inst.client.CreateSession(ctx, title, directory)
	})
}

func (m *OpenCodeManager) UpdateSessionTitle(ctx context.Context, instanceID, sessionID, title string) (*opencode.Session, error) {
	return m.runSessionMutation(ctx, instanceID, "update session title", func(inst *openCodeInstance) (*opencode.Session, error) {
		return inst.client.UpdateSessionTitle(ctx, sessionID, title)
	})
}

func (m *OpenCodeManager) syncSessions(ctx context.Context, inst *openCodeInstance, sessions []opencode.Session) (int, error) {
	count := 0
	for _, session := range sessions {
		if err := m.bridge.ensureOpenCodeSessionPortal(ctx, inst, session); err != nil {
			m.log().Warn().Err(err).Str("session", session.ID).Msg("Failed to sync OpenCode session")
			continue
		}
		count++
	}
	return count, nil
}

// ---------- event loop ----------

func (m *OpenCodeManager) startEventLoop(inst *openCodeInstance) {
	if inst == nil || m.bridge == nil || m.bridge.host == nil {
		return
	}
	login := m.bridge.host.Login()
	if login == nil || login.Bridge == nil {
		return
	}
	ctx, cancel := context.WithCancel(login.Bridge.BackgroundCtx)
	inst.cancel = cancel

	go m.runEventLoop(ctx, inst)
}

func (m *OpenCodeManager) runEventLoop(ctx context.Context, inst *openCodeInstance) {
	backoff := 2 * time.Second
	const maxBackoff = 2 * time.Minute

	for {
		if ctx.Err() != nil {
			return
		}
		connectStart := time.Now()
		events, errs := inst.client.StreamEvents(ctx)

		if sessions, err := inst.client.ListSessions(ctx); err == nil {
			if _, syncErr := m.syncSessions(ctx, inst, sessions); syncErr != nil {
				m.log().Warn().Err(syncErr).Str("instance", inst.cfg.ID).Msg("Failed to sync sessions after reconnect")
			}
		} else {
			m.log().Warn().Err(err).Str("instance", inst.cfg.ID).Msg("Failed to list sessions after reconnect")
		}
		m.setConnected(inst, true)

		if m.consumeEventStream(ctx, inst, events, errs) {
			return // context cancelled
		}

		m.setConnected(inst, false)
		if ctx.Err() != nil {
			return
		}

		if time.Since(connectStart) > 10*time.Second {
			backoff = 2 * time.Second
		} else if backoff < maxBackoff {
			backoff = min(backoff*2, maxBackoff)
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// consumeEventStream reads from the event/error channels until the stream ends
// or the context is cancelled. Returns true if context was cancelled.
func (m *OpenCodeManager) consumeEventStream(ctx context.Context, inst *openCodeInstance, events <-chan opencode.Event, errs <-chan error) bool {
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				return false
			}
			m.handleEvent(ctx, inst, evt)
		case err, ok := <-errs:
			if ok && err != nil {
				m.log().Warn().Err(err).Str("instance", inst.cfg.ID).Msg("Event stream error")
			}
			return false
		case <-ctx.Done():
			return true
		}
	}
}

// ---------- event dispatch ----------

func (m *OpenCodeManager) handleEvent(ctx context.Context, inst *openCodeInstance, evt opencode.Event) {
	switch evt.Type {
	case "session.created", "session.updated":
		m.handleSessionEvent(ctx, inst, evt)
	case "session.deleted":
		m.handleSessionDeleted(ctx, inst, evt)
	case "session.status":
		m.handleSessionStatusEvent(ctx, inst, evt)
	case "session.idle":
		m.handleSessionIdleEvent(ctx, inst, evt)
	case "message.updated":
		m.handleMessageUpdated(ctx, inst, evt)
	case "message.removed":
		m.handleMessageRemovedEvent(ctx, inst, evt)
	case "message.part.updated":
		m.handlePartUpdatedEvent(ctx, inst, evt)
	case "message.part.delta":
		m.handlePartDeltaEvent(ctx, inst, evt)
	case "message.part.removed":
		m.handlePartRemovedEvent(ctx, inst, evt)
	case "permission.asked":
		m.handlePermissionAskedEvent(ctx, inst, evt)
	case "permission.replied":
		m.handlePermissionRepliedEvent(ctx, inst, evt)
	case "question.asked":
		m.handleQuestionAskedEvent(ctx, inst, evt)
	case "question.replied", "question.rejected":
		// Question prompts are currently rejected by the bridge when asked.
	}
}

func (m *OpenCodeManager) handleSessionEvent(ctx context.Context, inst *openCodeInstance, evt opencode.Event) {
	var session opencode.Session
	if err := evt.DecodeInfo(&session); err != nil {
		m.log().Warn().Err(err).Msg("Failed to decode session event")
		return
	}
	if err := m.bridge.ensureOpenCodeSessionPortal(ctx, inst, session); err != nil {
		m.log().Warn().Err(err).Str("session", session.ID).Msg("Failed to ensure session portal")
	}
}

func (m *OpenCodeManager) handleSessionDeleted(ctx context.Context, inst *openCodeInstance, evt opencode.Event) {
	var session opencode.Session
	if err := evt.DecodeInfo(&session); err != nil {
		m.log().Warn().Err(err).Msg("Failed to decode session delete event")
		return
	}
	m.bridge.removeOpenCodeSessionPortal(ctx, inst.cfg.ID, session.ID, "opencode session deleted")
}

func (m *OpenCodeManager) handleSessionStatusEvent(ctx context.Context, inst *openCodeInstance, evt opencode.Event) {
	var payload struct {
		SessionID string `json:"sessionID"`
		Status    struct {
			Type string `json:"type"`
		} `json:"status"`
	}
	if err := json.Unmarshal(evt.Properties, &payload); err != nil {
		m.log().Warn().Err(err).Msg("Failed to decode session status event")
		return
	}
	if strings.EqualFold(strings.TrimSpace(payload.Status.Type), "idle") {
		m.processNextQueued(ctx, inst, payload.SessionID)
	}
}

func (m *OpenCodeManager) handleSessionIdleEvent(ctx context.Context, inst *openCodeInstance, evt opencode.Event) {
	var payload struct {
		SessionID string `json:"sessionID"`
	}
	if err := json.Unmarshal(evt.Properties, &payload); err != nil {
		m.log().Warn().Err(err).Msg("Failed to decode session idle event")
		return
	}
	m.processNextQueued(ctx, inst, payload.SessionID)
}

func (m *OpenCodeManager) handleMessageUpdated(ctx context.Context, inst *openCodeInstance, evt opencode.Event) {
	var msg opencode.Message
	if err := evt.DecodeInfo(&msg); err != nil {
		m.log().Warn().Err(err).Msg("Failed to decode message event")
		return
	}
	m.handleMessageEvent(ctx, inst, msg)
}

func (m *OpenCodeManager) handleMessageRemovedEvent(ctx context.Context, inst *openCodeInstance, evt opencode.Event) {
	var payload struct {
		SessionID string `json:"sessionID"`
		MessageID string `json:"messageID"`
	}
	if err := evt.DecodeInfo(&payload); err != nil {
		m.log().Warn().Err(err).Msg("Failed to decode message removal event")
		return
	}
	m.handleMessageRemoved(ctx, inst, payload.SessionID, payload.MessageID)
}

func (m *OpenCodeManager) handlePartUpdatedEvent(ctx context.Context, inst *openCodeInstance, evt opencode.Event) {
	var payload struct {
		Part  opencode.Part `json:"part"`
		Delta string        `json:"delta"`
	}
	if err := json.Unmarshal(evt.Properties, &payload); err != nil {
		m.log().Warn().Err(err).Msg("Failed to decode part update event")
		return
	}
	part := payload.Part
	if payload.Delta != "" && part.MessageID != "" {
		if full, err := inst.client.GetMessage(ctx, part.SessionID, part.MessageID); err == nil && full != nil {
			if refreshed, ok := findOpenCodePart(full.Parts, part.ID); ok {
				part = refreshed
			}
		}
	}
	m.handlePartUpdated(ctx, inst, part, payload.Delta)
}

func (m *OpenCodeManager) handlePartDeltaEvent(ctx context.Context, inst *openCodeInstance, evt opencode.Event) {
	var payload struct {
		SessionID string `json:"sessionID"`
		MessageID string `json:"messageID"`
		PartID    string `json:"partID"`
		Field     string `json:"field"`
		Delta     string `json:"delta"`
	}
	if err := json.Unmarshal(evt.Properties, &payload); err != nil {
		m.log().Warn().Err(err).Msg("Failed to decode part delta event")
		return
	}
	m.handlePartDelta(ctx, inst, payload.SessionID, payload.MessageID, payload.PartID, payload.Field, payload.Delta)
}

func (m *OpenCodeManager) handlePartRemovedEvent(ctx context.Context, inst *openCodeInstance, evt opencode.Event) {
	var payload struct {
		SessionID string `json:"sessionID"`
		MessageID string `json:"messageID"`
		PartID    string `json:"partID"`
	}
	if err := json.Unmarshal(evt.Properties, &payload); err != nil {
		m.log().Warn().Err(err).Msg("Failed to decode part removal event")
		return
	}
	m.handlePartRemoved(ctx, inst, payload.SessionID, payload.MessageID, payload.PartID)
}

func (m *OpenCodeManager) handlePermissionAskedEvent(ctx context.Context, inst *openCodeInstance, evt opencode.Event) {
	var req opencode.PermissionRequest
	if err := json.Unmarshal(evt.Properties, &req); err != nil {
		m.log().Warn().Err(err).Msg("Failed to decode permission request event")
		return
	}
	if req.ID == "" || req.SessionID == "" {
		return
	}
	portal := m.bridge.findOpenCodePortal(ctx, inst.cfg.ID, req.SessionID)
	if portal == nil {
		return
	}
	toolCallID := strings.TrimSpace(req.ID)
	messageID := ""
	if req.Tool != nil {
		if strings.TrimSpace(req.Tool.CallID) != "" {
			toolCallID = strings.TrimSpace(req.Tool.CallID)
		}
		messageID = strings.TrimSpace(req.Tool.MessageID)
	}
	if messageID == "" {
		m.log().Warn().
			Str("instance", inst.cfg.ID).
			Str("session", req.SessionID).
			Str("permission_id", req.ID).
			Msg("Skipping permission request without message id")
		return
	}
	approvalID := strings.TrimSpace(req.ID)
	_, created := m.approvals.Register(approvalID, 10*time.Minute, &permissionApprovalRef{
		RoomID:       portal.MXID,
		InstanceID:   inst.cfg.ID,
		SessionID:    req.SessionID,
		MessageID:    messageID,
		ToolCallID:   toolCallID,
		PermissionID: approvalID,
	})
	if !created {
		return
	}
	toolName := strings.TrimSpace(req.Permission)
	if toolName == "" {
		toolName = "tool"
	}
	m.ensureStepStarted(ctx, inst, portal, req.SessionID, messageID)
	turnID := opencodeMessageStreamTurnID(req.SessionID, messageID)
	m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, m.bridge.portalAgentID(portal), map[string]any{
		"type":       "tool-approval-request",
		"approvalId": approvalID,
		"toolCallId": toolCallID,
		"toolName":   toolName,
	})
}

func (m *OpenCodeManager) handlePermissionRepliedEvent(ctx context.Context, inst *openCodeInstance, evt opencode.Event) {
	var payload struct {
		SessionID string `json:"sessionID"`
		RequestID string `json:"requestID"`
		Reply     string `json:"reply"`
	}
	if err := json.Unmarshal(evt.Properties, &payload); err != nil {
		m.log().Warn().Err(err).Msg("Failed to decode permission reply event")
		return
	}
	pending := m.approvals.Get(strings.TrimSpace(payload.RequestID))
	if pending == nil {
		return
	}
	ref, _ := pending.Data.(*permissionApprovalRef)
	if ref == nil {
		m.approvals.Drop(payload.RequestID)
		return
	}
	if strings.EqualFold(strings.TrimSpace(payload.Reply), "reject") {
		portal := m.bridge.findOpenCodePortal(ctx, inst.cfg.ID, ref.SessionID)
		if portal != nil {
			m.ensureStepStarted(ctx, inst, portal, ref.SessionID, ref.MessageID)
			turnID := opencodeMessageStreamTurnID(ref.SessionID, ref.MessageID)
			m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, m.bridge.portalAgentID(portal), map[string]any{
				"type":       "tool-output-denied",
				"toolCallId": ref.ToolCallID,
			})
		}
	}
	m.approvals.Drop(payload.RequestID)
}

func (m *OpenCodeManager) handleQuestionAskedEvent(ctx context.Context, inst *openCodeInstance, evt opencode.Event) {
	var req opencode.QuestionRequest
	if err := json.Unmarshal(evt.Properties, &req); err != nil {
		m.log().Warn().Err(err).Msg("Failed to decode question request event")
		return
	}
	if req.ID == "" || req.SessionID == "" {
		return
	}
	portal := m.bridge.findOpenCodePortal(ctx, inst.cfg.ID, req.SessionID)
	if portal != nil {
		m.bridge.host.SendSystemNotice(ctx, portal, "OpenCode question requests are not yet supported in the Matrix bridge.")
		if req.Tool != nil && strings.TrimSpace(req.Tool.CallID) != "" && strings.TrimSpace(req.Tool.MessageID) != "" {
			m.ensureStepStarted(ctx, inst, portal, req.SessionID, strings.TrimSpace(req.Tool.MessageID))
			turnID := opencodeMessageStreamTurnID(req.SessionID, strings.TrimSpace(req.Tool.MessageID))
			m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, m.bridge.portalAgentID(portal), map[string]any{
				"type":       "tool-output-error",
				"toolCallId": strings.TrimSpace(req.Tool.CallID),
				"errorText":  "Question requests are not supported by the Matrix bridge.",
			})
		}
	}
	if err := inst.client.RejectQuestion(ctx, req.ID); err != nil {
		m.log().Warn().Err(err).
			Str("instance", inst.cfg.ID).
			Str("session", req.SessionID).
			Str("request_id", req.ID).
			Msg("Failed to reject unsupported question request")
	}
}

func (m *OpenCodeManager) resolvePermissionDecision(ctx context.Context, roomID id.RoomID, approvalID string, decision permissionDecision) error {
	if m == nil || m.bridge == nil || m.bridge.host == nil {
		return errors.New("bridge not available")
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return bridgeadapter.ErrApprovalMissingID
	}
	if strings.TrimSpace(roomID.String()) == "" {
		return bridgeadapter.ErrApprovalMissingRoom
	}
	login := m.bridge.host.Login()
	if login == nil || decision.DecidedBy == "" || decision.DecidedBy != login.UserMXID {
		return bridgeadapter.ErrApprovalOnlyOwner
	}
	pending := m.approvals.Get(approvalID)
	if pending == nil {
		return fmt.Errorf("%w: %s", bridgeadapter.ErrApprovalUnknown, approvalID)
	}
	ref, _ := pending.Data.(*permissionApprovalRef)
	if ref == nil {
		m.approvals.Drop(approvalID)
		return fmt.Errorf("%w: %s", bridgeadapter.ErrApprovalUnknown, approvalID)
	}
	if ref.RoomID != "" && ref.RoomID != roomID {
		return bridgeadapter.ErrApprovalWrongRoom
	}
	inst, err := m.requireConnectedInstance(ref.InstanceID)
	if err != nil {
		return err
	}
	if err := inst.client.RespondPermission(ctx, ref.SessionID, ref.PermissionID, decision.Response); err != nil {
		if opencode.IsAuthError(err) {
			m.setConnected(inst, false)
		}
		return fmt.Errorf("respond to permission: %w", err)
	}
	m.approvals.Drop(approvalID)
	if decision.Response == "reject" {
		portal := m.bridge.findOpenCodePortal(ctx, ref.InstanceID, ref.SessionID)
		if portal != nil {
			m.ensureStepStarted(ctx, inst, portal, ref.SessionID, ref.MessageID)
			turnID := opencodeMessageStreamTurnID(ref.SessionID, ref.MessageID)
			m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, m.bridge.portalAgentID(portal), map[string]any{
				"type":       "tool-output-denied",
				"toolCallId": ref.ToolCallID,
			})
		}
	}
	return nil
}

// ---------- message/part processing ----------

func (m *OpenCodeManager) handleMessageEvent(ctx context.Context, inst *openCodeInstance, msg opencode.Message) {
	if msg.ID == "" || msg.SessionID == "" {
		return
	}
	isCompleted := msg.Time.Completed != 0

	if inst.isSeen(msg.SessionID, msg.ID) {
		if isCompleted && msg.Role != "user" {
			if portal := m.bridge.findOpenCodePortal(ctx, inst.cfg.ID, msg.SessionID); portal != nil && inst.turnStateFor(msg.SessionID, msg.ID) != nil {
				if full, err := inst.client.GetMessage(ctx, msg.SessionID, msg.ID); err == nil && full != nil {
					m.ensureTurnStarted(ctx, inst, portal, msg.SessionID, msg.ID, buildTurnStartMetadata(full, m.bridge.portalAgentID(portal)))
					m.emitTurnFinish(ctx, inst, portal, msg.SessionID, msg.ID, "stop", buildTurnFinishMetadata(full, m.bridge.portalAgentID(portal), "stop"))
				} else {
					m.emitTurnFinish(ctx, inst, portal, msg.SessionID, msg.ID, "stop", nil)
				}
			}
		}
		return
	}
	full, err := inst.client.GetMessage(ctx, msg.SessionID, msg.ID)
	if err != nil {
		m.log().Warn().Err(err).Str("message", msg.ID).Msg("Failed to fetch message")
		return
	}
	if msg.Role == "user" {
		inst.markSeen(msg.SessionID, msg.ID, msg.Role)
		return
	}
	inst.markSeen(msg.SessionID, msg.ID, msg.Role)
	portal := m.bridge.findOpenCodePortal(ctx, inst.cfg.ID, msg.SessionID)
	if portal == nil {
		return
	}
	m.ensureTurnStarted(ctx, inst, portal, msg.SessionID, msg.ID, buildTurnStartMetadata(full, m.bridge.portalAgentID(portal)))
	m.handleMessageParts(ctx, inst, portal, msg.Role, full)
	if isCompleted {
		m.emitTurnFinish(ctx, inst, portal, msg.SessionID, msg.ID, "stop", buildTurnFinishMetadata(full, m.bridge.portalAgentID(portal), "stop"))
	}
}

func (m *OpenCodeManager) handleMessageParts(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, role string, msg *opencode.MessageWithParts) {
	if msg == nil || portal == nil {
		return
	}
	if role == "user" {
		if msg.Info.ID != "" && msg.Info.SessionID != "" {
			inst.markSeen(msg.Info.SessionID, msg.Info.ID, role)
		}
		return
	}
	inst.upsertMessage(msg.Info.SessionID, *msg)
	for _, part := range msg.Parts {
		if part.MessageID == "" {
			part.MessageID = msg.Info.ID
		}
		if part.SessionID == "" {
			part.SessionID = msg.Info.SessionID
		}
		m.syncAssistantMessagePart(ctx, inst, portal, msg, part)
		m.handlePart(ctx, inst, portal, role, part, false)
	}
}

func (m *OpenCodeManager) handlePartUpdated(ctx context.Context, inst *openCodeInstance, part opencode.Part, delta string) {
	if part.ID == "" || part.SessionID == "" {
		return
	}
	portal := m.bridge.findOpenCodePortal(ctx, inst.cfg.ID, part.SessionID)
	if portal == nil {
		return
	}
	inst.upsertPart(part.SessionID, part.MessageID, part)
	role := m.resolvePartRole(ctx, inst, part)
	if role == "user" {
		return
	}
	if part.Type == "tool" && delta != "" {
		m.emitToolStreamDelta(ctx, inst, portal, part, delta)
	}
	if part.Type == "text" && delta != "" {
		m.emitTextStreamDelta(ctx, inst, portal, part, delta)
	}
	if part.Type == "reasoning" && delta != "" {
		m.emitReasoningStreamDelta(ctx, inst, portal, part, delta)
	}
	m.emitTextStreamEnd(ctx, inst, portal, part)
	m.handlePart(ctx, inst, portal, role, part, true)
}

// resolvePartRole determines the role for a part, fetching the full message if needed.
func (m *OpenCodeManager) resolvePartRole(ctx context.Context, inst *openCodeInstance, part opencode.Part) string {
	role := inst.seenRole(part.SessionID, part.MessageID)
	if role == "user" && inst.isSeen(part.SessionID, part.MessageID) {
		return "user"
	}
	if role == "" && part.MessageID != "" {
		if full, err := inst.client.GetMessage(ctx, part.SessionID, part.MessageID); err == nil && full != nil {
			role = full.Info.Role
			if role != "" {
				inst.markSeen(part.SessionID, part.MessageID, role)
			}
		}
	}
	if role == "" {
		role = "assistant"
	}
	if role == "user" && part.MessageID != "" {
		inst.markSeen(part.SessionID, part.MessageID, role)
	}
	return role
}

func (m *OpenCodeManager) handlePartDelta(ctx context.Context, inst *openCodeInstance, sessionID, messageID, partID, field, delta string) {
	if sessionID == "" || partID == "" || delta == "" {
		return
	}
	portal := m.bridge.findOpenCodePortal(ctx, inst.cfg.ID, sessionID)
	if portal == nil {
		return
	}
	role := inst.seenRole(sessionID, messageID)
	if role == "user" && inst.isSeen(sessionID, messageID) {
		return
	}
	if role == "" {
		role = "assistant"
	}

	part := opencode.Part{
		ID:        partID,
		SessionID: sessionID,
		MessageID: messageID,
		Type:      field,
	}
	inst.ensurePartState(sessionID, messageID, partID, role, field)

	switch field {
	case "text":
		m.emitTextStreamDelta(ctx, inst, portal, part, delta)
	case "reasoning":
		m.emitReasoningStreamDelta(ctx, inst, portal, part, delta)
	case "tool":
		m.emitToolStreamDelta(ctx, inst, portal, part, delta)
	}
}

func (m *OpenCodeManager) handlePartRemoved(ctx context.Context, inst *openCodeInstance, sessionID, messageID, partID string) {
	if sessionID == "" || partID == "" {
		return
	}
	portal := m.bridge.findOpenCodePortal(ctx, inst.cfg.ID, sessionID)
	if portal == nil {
		return
	}
	inst.removeCachedPart(sessionID, messageID, partID)
	role := inst.seenRole(sessionID, messageID)
	partType := ""
	if state := inst.partState(sessionID, partID); state != nil {
		if state.role != "" {
			role = state.role
		}
		partType = state.partType
	}
	m.bridge.emitOpenCodePartRemove(ctx, portal, inst.cfg.ID, partID, partType, role == "user")
	inst.removePart(sessionID, messageID, partID)
}

func (m *OpenCodeManager) handlePart(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, role string, part opencode.Part, allowEdit bool) {
	if part.ID == "" || part.SessionID == "" {
		return
	}
	if part.Type == "tool" {
		m.handleToolPart(ctx, inst, portal, role, part)
		return
	}
	state := inst.partState(part.SessionID, part.ID)
	if state == nil {
		inst.ensurePartState(part.SessionID, part.MessageID, part.ID, role, part.Type)
		if part.Type == "file" {
			m.emitArtifactStream(ctx, inst, portal, part)
			return
		}
		if role != "user" {
			if part.Type == "text" || part.Type == "reasoning" {
				m.emitTextStreamEnd(ctx, inst, portal, part)
				return
			}
			m.emitDataPartStream(ctx, inst, portal, part)
			return
		}
		m.bridge.emitOpenCodePart(ctx, portal, inst.cfg.ID, part, role == "user")
		return
	}
	if part.Type == "file" {
		m.emitArtifactStream(ctx, inst, portal, part)
		return
	}
	if role != "user" {
		if part.Type == "text" || part.Type == "reasoning" {
			m.emitTextStreamEnd(ctx, inst, portal, part)
			return
		}
		m.emitDataPartStream(ctx, inst, portal, part)
		return
	}
	if allowEdit && (part.Type == "text" || part.Type == "reasoning") {
		m.bridge.emitOpenCodePartEdit(ctx, portal, inst.cfg.ID, part, role == "user")
	}
	if part.Type == "text" || part.Type == "reasoning" {
		m.emitTextStreamEnd(ctx, inst, portal, part)
	}
}

func (m *OpenCodeManager) handleToolPart(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, role string, part opencode.Part) {
	state := inst.ensurePartState(part.SessionID, part.MessageID, part.ID, role, part.Type)
	if state == nil {
		return
	}
	status := ""
	if part.State != nil {
		status = part.State.Status
	}
	m.emitToolStreamState(ctx, inst, portal, part, status)
	callSent, resultSent := inst.partFlags(part.SessionID, part.ID)
	callStatus := inst.partCallStatus(part.SessionID, part.ID)
	if !callSent && status != "" {
		inst.setPartCallSent(part.SessionID, part.ID)
		inst.setPartCallStatus(part.SessionID, part.ID, status)
	} else if callSent && status != "" && status != callStatus {
		inst.setPartCallStatus(part.SessionID, part.ID, status)
	}
	if !resultSent && (status == "completed" || status == "error") {
		inst.setPartResultSent(part.SessionID, part.ID)
	}
	if part.State == nil || len(part.State.Attachments) == 0 {
		return
	}
	for _, attachment := range part.State.Attachments {
		if attachment.ID == "" {
			continue
		}
		if attachment.SessionID == "" {
			attachment.SessionID = part.SessionID
		}
		if attachment.MessageID == "" {
			attachment.MessageID = part.MessageID
		}
		m.handlePart(ctx, inst, portal, role, attachment, false)
	}
}

func (m *OpenCodeManager) handleMessageRemoved(ctx context.Context, inst *openCodeInstance, sessionID, messageID string) {
	if sessionID == "" || messageID == "" {
		return
	}
	portal := m.bridge.findOpenCodePortal(ctx, inst.cfg.ID, sessionID)
	if portal == nil {
		return
	}
	inst.removeCachedMessage(sessionID, messageID)
	role := inst.seenRole(sessionID, messageID)
	partStates := inst.messageParts(sessionID, messageID)
	if role != "user" {
		m.bridge.emitOpenCodeMessageRemove(ctx, portal, inst.cfg.ID, messageID, false)
	}
	for partID := range partStates {
		inst.removePart(sessionID, messageID, partID)
	}
	inst.removeTurnState(sessionID, messageID)
}

// ---------- connection state management ----------

const disconnectGracePeriod = 5 * time.Second

func (m *OpenCodeManager) setConnected(inst *openCodeInstance, connected bool) {
	if inst == nil {
		return
	}

	inst.disconnectMu.Lock()
	defer inst.disconnectMu.Unlock()

	if connected {
		if inst.disconnectTimer != nil {
			inst.disconnectTimer.Stop()
			inst.disconnectTimer = nil
		}
		if inst.connected {
			return
		}
		inst.connected = true
		m.applyConnectedState(inst, true)
		return
	}

	if !inst.connected {
		return
	}
	inst.connected = false

	if inst.disconnectTimer != nil {
		inst.disconnectTimer.Stop()
	}
	inst.disconnectTimer = time.AfterFunc(disconnectGracePeriod, func() {
		inst.disconnectMu.Lock()
		defer inst.disconnectMu.Unlock()
		inst.disconnectTimer = nil
		if inst.connected {
			return
		}
		m.applyConnectedState(inst, false)
	})
}

func (m *OpenCodeManager) applyConnectedState(inst *openCodeInstance, connected bool) {
	if m.bridge == nil || m.bridge.host == nil {
		return
	}
	login := m.bridge.host.Login()
	if login == nil || login.Bridge == nil {
		return
	}
	ctx := login.Bridge.BackgroundCtx
	portals, err := m.bridge.listAllChatPortals(ctx)
	if err != nil {
		return
	}
	for _, portal := range portals {
		if portal == nil {
			continue
		}
		meta := m.bridge.portalMeta(portal)
		if meta == nil || !meta.IsOpenCodeRoom || meta.InstanceID != inst.cfg.ID {
			continue
		}
		if meta.ReadOnly == !connected {
			continue
		}
		meta.ReadOnly = !connected
		m.bridge.host.SetPortalMeta(portal, meta)
		_ = m.bridge.host.SavePortal(ctx, portal)
		if connected {
			m.bridge.host.SendSystemNotice(ctx, portal, "OpenCode reconnected. You can send messages again.")
		} else {
			m.bridge.host.SendSystemNotice(ctx, portal, "OpenCode disconnected. This room is now read-only until it reconnects.")
		}
	}
}

// ---------- utilities ----------

func opencodeMessageIDForEvent(eventID id.EventID) string {
	trimmed := strings.TrimSpace(string(eventID))
	if trimmed == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(trimmed))
	return "msg_mx_" + hex.EncodeToString(hash[:8])
}

func findOpenCodePart(parts []opencode.Part, partID string) (opencode.Part, bool) {
	for _, part := range parts {
		if part.ID == partID {
			return part, true
		}
	}
	return opencode.Part{}, false
}
