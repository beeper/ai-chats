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
)

// OpenCodeManager coordinates connections to OpenCode server instances,
// dispatches SSE events, and manages session lifecycle.
type OpenCodeManager struct {
	bridge    *Bridge
	mu        sync.RWMutex
	instances map[string]*openCodeInstance
}

func NewOpenCodeManager(bridge *Bridge) *OpenCodeManager {
	return &OpenCodeManager{
		bridge:    bridge,
		instances: make(map[string]*openCodeInstance),
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
		if inst.cancel != nil {
			inst.cancel()
		}
		inst.cancel = nil
		inst.connected = false
		inst.disconnectMu.Lock()
		if inst.disconnectTimer != nil {
			inst.disconnectTimer.Stop()
			inst.disconnectTimer = nil
		}
		inst.disconnectMu.Unlock()
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
		if _, _, err := m.Connect(ctx, cfg.URL, cfg.Password, cfg.Username); err != nil {
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
			ID:       instanceID,
			URL:      normalized,
			Username: user,
			Password: strings.TrimSpace(password),
		},
		client:         client,
		connected:      true,
		seenMsg:        make(map[string]map[string]string),
		seenPart:       make(map[string]map[string]*openCodePartState),
		partsByMessage: make(map[string]map[string]map[string]struct{}),
		turnState:      make(map[string]map[string]*openCodeTurnState),
	}

	m.mu.Lock()
	if existing := m.instances[instanceID]; existing != nil {
		if existing.cancel != nil {
			existing.cancel()
		}
		existing.disconnectMu.Lock()
		if existing.disconnectTimer != nil {
			existing.disconnectTimer.Stop()
			existing.disconnectTimer = nil
		}
		existing.disconnectMu.Unlock()
	}
	m.instances[instanceID] = inst
	m.mu.Unlock()

	m.persistInstance(ctx, inst)
	m.bridge.ensureOpenCodeGhostDisplayName(ctx, instanceID)

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
		ID:       inst.cfg.ID,
		URL:      inst.cfg.URL,
		Username: inst.cfg.Username,
		Password: inst.cfg.Password,
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
		if inst.cancel != nil {
			inst.cancel()
		}
		inst.disconnectMu.Lock()
		if inst.disconnectTimer != nil {
			inst.disconnectTimer.Stop()
			inst.disconnectTimer = nil
		}
		inst.disconnectMu.Unlock()
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
		inst.markSeen(sessionID, msgID, "user")
	}

	if err := inst.client.SendMessageAsync(ctx, sessionID, msgID, parts); err != nil {
		if opencode.IsAuthError(err) {
			m.setConnected(inst, false)
		}
		return fmt.Errorf("send message: %w", err)
	}
	return nil
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

func (m *OpenCodeManager) CreateSession(ctx context.Context, instanceID, title, directory string) (*opencode.Session, error) {
	inst, err := m.requireConnectedInstance(instanceID)
	if err != nil {
		return nil, err
	}
	session, err := inst.client.CreateSession(ctx, title, directory)
	if err != nil {
		if opencode.IsAuthError(err) {
			m.setConnected(inst, false)
		}
		return nil, fmt.Errorf("create session: %w", err)
	}
	return session, nil
}

func (m *OpenCodeManager) UpdateSessionTitle(ctx context.Context, instanceID, sessionID, title string) (*opencode.Session, error) {
	inst, err := m.requireConnectedInstance(instanceID)
	if err != nil {
		return nil, err
	}
	session, err := inst.client.UpdateSessionTitle(ctx, sessionID, title)
	if err != nil {
		if opencode.IsAuthError(err) {
			m.setConnected(inst, false)
		}
		return nil, fmt.Errorf("update session title: %w", err)
	}
	return session, nil
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

// ---------- message/part processing ----------

func (m *OpenCodeManager) handleMessageEvent(ctx context.Context, inst *openCodeInstance, msg opencode.Message) {
	if msg.ID == "" || msg.SessionID == "" {
		return
	}
	isCompleted := msg.Time.Completed != 0

	if inst.isSeen(msg.SessionID, msg.ID) {
		if isCompleted && msg.Role != "user" {
			if portal := m.bridge.findOpenCodePortal(ctx, inst.cfg.ID, msg.SessionID); portal != nil {
				m.emitTurnFinish(ctx, inst, portal, msg.SessionID, msg.ID, "stop")
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
	m.handleMessageParts(ctx, inst, portal, msg.Role, full)
	if isCompleted {
		m.emitTurnFinish(ctx, inst, portal, msg.SessionID, msg.ID, "stop")
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
		m.bridge.emitOpenCodePart(ctx, portal, inst.cfg.ID, part, role == "user")
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
		m.bridge.emitOpenCodeToolCall(ctx, portal, inst.cfg.ID, part, role == "user", status)
		inst.setPartCallSent(part.SessionID, part.ID)
		inst.setPartCallStatus(part.SessionID, part.ID, status)
	} else if callSent && status != "" && status != callStatus {
		inst.setPartCallStatus(part.SessionID, part.ID, status)
	}

	reaction := opencodeToolStatusReaction(part, status)
	prevReaction := inst.partStatusReaction(part.SessionID, part.ID)
	if reaction != prevReaction {
		if prevReaction != "" {
			m.bridge.emitOpenCodeToolStatusReactionRemove(ctx, portal, inst.cfg.ID, part, role == "user", prevReaction)
			inst.setPartStatusReaction(part.SessionID, part.ID, "")
		}
		if reaction != "" {
			m.bridge.emitOpenCodeToolStatusReaction(ctx, portal, inst.cfg.ID, part, role == "user", reaction)
			inst.setPartStatusReaction(part.SessionID, part.ID, reaction)
		}
	}
	if !resultSent && (status == "completed" || status == "error") {
		m.bridge.emitOpenCodeToolResult(ctx, portal, inst.cfg.ID, part, role == "user", status)
		inst.setPartResultSent(part.SessionID, part.ID)
	}
	if status == "completed" || status == "error" {
		if prev := inst.partStatusReaction(part.SessionID, part.ID); prev != "" {
			m.bridge.emitOpenCodeToolStatusReactionRemove(ctx, portal, inst.cfg.ID, part, role == "user", prev)
			inst.setPartStatusReaction(part.SessionID, part.ID, "")
		}
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
	if len(partStates) == 0 {
		m.bridge.emitOpenCodeMessageRemove(ctx, portal, inst.cfg.ID, messageID, role == "user")
		return
	}
	for partID, state := range partStates {
		removedRole := role
		partType := ""
		if state != nil {
			if state.role != "" {
				removedRole = state.role
			}
			partType = state.partType
		}
		m.bridge.emitOpenCodePartRemove(ctx, portal, inst.cfg.ID, partID, partType, removedRole == "user")
		inst.removePart(sessionID, messageID, partID)
	}
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
