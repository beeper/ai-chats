package opencodebridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/bridges/opencode/opencode"
)

type OpenCodeManager struct {
	bridge    *Bridge
	mu        sync.RWMutex
	instances map[string]*openCodeInstance
}

type openCodeInstance struct {
	cfg       OpenCodeInstance
	client    *opencode.Client
	connected bool
	cancel    context.CancelFunc

	disconnectMu    sync.Mutex
	disconnectTimer *time.Timer

	seenMu         sync.Mutex
	seenMsg        map[string]map[string]string
	seenPart       map[string]map[string]*openCodePartState
	partsByMessage map[string]map[string]map[string]struct{}

	cacheMu      sync.Mutex
	messageCache map[string]*openCodeMessageCache

	turnState map[string]map[string]*openCodeTurnState
}

type openCodeTurnState struct {
	started  bool
	stepOpen bool
	finished bool
}

type openCodePartState struct {
	role                   string
	messageID              string
	partType               string
	callStatus             string
	statusReaction         string
	callSent               bool
	resultSent             bool
	textStreamStarted      bool
	textStreamEnded        bool
	reasoningStreamStarted bool
	reasoningStreamEnded   bool
	streamInputStarted     bool
	streamInputAvailable   bool
	streamOutputAvailable  bool
	streamOutputError      bool
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
	if inst == nil {
		return false
	}
	return inst.connected
}

// DisconnectAll stops all in-memory OpenCode connections/event loops without
// modifying persisted instance metadata. Connections will be restored on next login connect.
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
	meta := m.bridge.host.OpenCodeInstances()
	if len(meta) == 0 {
		return nil
	}
	for _, cfg := range meta {
		if cfg == nil || strings.TrimSpace(cfg.URL) == "" {
			continue
		}
		_, _, err := m.Connect(ctx, cfg.URL, cfg.Password, cfg.Username)
		if err != nil {
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
		return nil, 0, err
	}
	instanceID := OpenCodeInstanceID(normalized, user)

	client, err := opencode.NewClient(normalized, user, password)
	if err != nil {
		return nil, 0, err
	}

	sessions, err := client.ListSessions(ctx)
	if err != nil {
		return nil, 0, err
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
	}
	m.instances[instanceID] = inst
	m.mu.Unlock()

	meta := m.bridge.host.OpenCodeInstances()
	if meta == nil {
		meta = make(map[string]*OpenCodeInstance)
	}
	meta[instanceID] = &OpenCodeInstance{
		ID:       instanceID,
		URL:      normalized,
		Username: user,
		Password: strings.TrimSpace(password),
	}
	if err := m.bridge.host.SaveOpenCodeInstances(ctx, meta); err != nil {
		m.log().Warn().Err(err).Msg("Failed to persist OpenCode instance")
	}

	m.bridge.ensureOpenCodeGhostDisplayName(ctx, instanceID)

	count, syncErr := m.syncSessions(ctx, inst, sessions)
	m.startEventLoop(inst)
	return inst, count, syncErr
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

func (m *OpenCodeManager) RemoveInstance(ctx context.Context, instanceID string) error {
	if m == nil || m.bridge == nil || m.bridge.host == nil {
		return errors.New("opencode manager unavailable")
	}
	id := strings.TrimSpace(instanceID)
	if id == "" {
		return errors.New("instance id is required")
	}

	// Read the instance before teardown so we can clean up portals/sessions
	// while the client is still functional.
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
			return nil // already sent
		}
		inst.markSeen(sessionID, msgID, "user")
	}

	if err := inst.client.SendMessageAsync(ctx, sessionID, msgID, parts); err != nil {
		if opencode.IsAuthError(err) {
			m.setConnected(inst, false)
		}
		return err
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

func (m *OpenCodeManager) CreateSession(ctx context.Context, instanceID, title string) (*opencode.Session, error) {
	inst, err := m.requireConnectedInstance(instanceID)
	if err != nil {
		return nil, err
	}
	session, err := inst.client.CreateSession(ctx, title)
	if err != nil {
		if opencode.IsAuthError(err) {
			m.setConnected(inst, false)
		}
		return nil, err
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
		return nil, err
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

	go func() {
		backoff := 2 * time.Second
		maxBackoff := 2 * time.Minute
		for {
			if ctx.Err() != nil {
				return
			}
			connectStart := time.Now()
			events, errs := inst.client.StreamEvents(ctx)

			sessions, err := inst.client.ListSessions(ctx)
			if err == nil {
				if _, syncErr := m.syncSessions(ctx, inst, sessions); syncErr != nil {
					m.log().Warn().Err(syncErr).Str("instance", inst.cfg.ID).Msg("Failed to sync OpenCode sessions after reconnect")
				}
			} else {
				m.log().Warn().Err(err).Str("instance", inst.cfg.ID).Msg("Failed to list OpenCode sessions after reconnect")
			}
			m.setConnected(inst, true)

			streamEnded := false
			for !streamEnded {
				select {
				case evt, ok := <-events:
					if !ok {
						streamEnded = true
						break
					}
					m.handleEvent(ctx, inst, evt)
				case err, ok := <-errs:
					if ok && err != nil {
						m.log().Warn().Err(err).Str("instance", inst.cfg.ID).Msg("OpenCode event stream error")
					}
					streamEnded = true
				case <-ctx.Done():
					return
				}
			}

			m.setConnected(inst, false)
			if ctx.Err() != nil {
				return
			}

			if time.Since(connectStart) > 10*time.Second {
				backoff = 2 * time.Second
			} else if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}

			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

func (m *OpenCodeManager) handleEvent(ctx context.Context, inst *openCodeInstance, evt opencode.Event) {
	switch evt.Type {
	case "session.created", "session.updated":
		var session opencode.Session
		if err := evt.DecodeInfo(&session); err != nil {
			m.log().Warn().Err(err).Msg("Failed to decode OpenCode session event")
			return
		}
		if err := m.bridge.ensureOpenCodeSessionPortal(ctx, inst, session); err != nil {
			m.log().Warn().Err(err).Str("session", session.ID).Msg("Failed to ensure OpenCode session portal")
		}
	case "session.deleted":
		var session opencode.Session
		if err := evt.DecodeInfo(&session); err != nil {
			m.log().Warn().Err(err).Msg("Failed to decode OpenCode session delete event")
			return
		}
		m.bridge.removeOpenCodeSessionPortal(ctx, inst.cfg.ID, session.ID, "opencode session deleted")
	case "message.updated":
		var msg opencode.Message
		if err := evt.DecodeInfo(&msg); err != nil {
			m.log().Warn().Err(err).Msg("Failed to decode OpenCode message event")
			return
		}
		m.handleMessageEvent(ctx, inst, msg)
	case "message.removed":
		var payload struct {
			SessionID string `json:"sessionID"`
			MessageID string `json:"messageID"`
		}
		if err := evt.DecodeInfo(&payload); err != nil {
			m.log().Warn().Err(err).Msg("Failed to decode OpenCode message removal event")
			return
		}
		m.handleMessageRemoved(ctx, inst, payload.SessionID, payload.MessageID)
	case "message.part.updated":
		var payload struct {
			Part  opencode.Part `json:"part"`
			Delta string        `json:"delta"`
		}
		if err := json.Unmarshal(evt.Properties, &payload); err != nil {
			m.log().Warn().Err(err).Msg("Failed to decode OpenCode part update event")
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
	case "message.part.removed":
		var payload struct {
			SessionID string `json:"sessionID"`
			MessageID string `json:"messageID"`
			PartID    string `json:"partID"`
		}
		if err := json.Unmarshal(evt.Properties, &payload); err != nil {
			m.log().Warn().Err(err).Msg("Failed to decode OpenCode part removal event")
			return
		}
		m.handlePartRemoved(ctx, inst, payload.SessionID, payload.MessageID, payload.PartID)
	}
}

func (m *OpenCodeManager) handleMessageEvent(ctx context.Context, inst *openCodeInstance, msg opencode.Message) {
	if msg.ID == "" || msg.SessionID == "" {
		return
	}
	isCompleted := msg.Time.Completed != 0

	if inst.isSeen(msg.SessionID, msg.ID) {
		if isCompleted && msg.Role != "user" {
			portal := m.bridge.findOpenCodePortal(ctx, inst.cfg.ID, msg.SessionID)
			if portal != nil {
				m.emitTurnFinish(ctx, inst, portal, msg.SessionID, msg.ID, "stop")
			}
		}
		return
	}
	full, err := inst.client.GetMessage(ctx, msg.SessionID, msg.ID)
	if err != nil {
		m.log().Warn().Err(err).Str("message", msg.ID).Msg("Failed to fetch OpenCode message")
		return
	}
	if msg.Role == "user" {
		// Do not echo user-role messages back into Matrix.
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
	role := inst.seenRole(part.SessionID, part.MessageID)
	if role == "user" && inst.isSeen(part.SessionID, part.MessageID) {
		return
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
	if role == "user" {
		if part.MessageID != "" {
			inst.markSeen(part.SessionID, part.MessageID, role)
		}
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
		prev := inst.partStatusReaction(part.SessionID, part.ID)
		if prev != "" {
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
	// Remove legacy message ID if it exists from older bridges.
	m.bridge.emitOpenCodeMessageRemove(ctx, portal, inst.cfg.ID, messageID, role == "user")
}

const disconnectGracePeriod = 5 * time.Second

func (m *OpenCodeManager) setConnected(inst *openCodeInstance, connected bool) {
	if inst == nil {
		return
	}

	inst.disconnectMu.Lock()
	defer inst.disconnectMu.Unlock()

	if connected {
		// Reconnected: cancel any pending disconnect timer silently.
		if inst.disconnectTimer != nil {
			inst.disconnectTimer.Stop()
			inst.disconnectTimer = nil
		}
		if inst.connected {
			// Already marked connected — nothing to broadcast.
			return
		}
		inst.connected = true
		m.applyConnectedState(inst, true)
		return
	}

	// Disconnect: start a grace-period timer before notifying rooms.
	if !inst.connected {
		return // already disconnected
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
			return // reconnected during grace period
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
		if !connected {
			m.bridge.host.SendSystemNotice(ctx, portal, "OpenCode disconnected. This room is now read-only until it reconnects.")
		} else {
			m.bridge.host.SendSystemNotice(ctx, portal, "OpenCode reconnected. You can send messages again.")
		}
	}
}

func (inst *openCodeInstance) isSeen(sessionID, messageID string) bool {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if inst.seenMsg == nil {
		inst.seenMsg = make(map[string]map[string]string)
	}
	seen, ok := inst.seenMsg[sessionID]
	if !ok {
		return false
	}
	_, exists := seen[messageID]
	return exists
}

func (inst *openCodeInstance) markSeen(sessionID, messageID, role string) {
	if messageID == "" || sessionID == "" {
		return
	}
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if inst.seenMsg == nil {
		inst.seenMsg = make(map[string]map[string]string)
	}
	seen, ok := inst.seenMsg[sessionID]
	if !ok {
		seen = make(map[string]string)
		inst.seenMsg[sessionID] = seen
	}
	seen[messageID] = role
}

func (inst *openCodeInstance) seenRole(sessionID, messageID string) string {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if inst.seenMsg == nil {
		return ""
	}
	seen, ok := inst.seenMsg[sessionID]
	if !ok {
		return ""
	}
	return seen[messageID]
}

// withPartState calls fn with the part state for the given session and part,
// if it exists. Caller should NOT hold inst.seenMu.
func (inst *openCodeInstance) withPartState(sessionID, partID string, fn func(ps *openCodePartState)) {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if inst.seenPart == nil {
		return
	}
	if parts, ok := inst.seenPart[sessionID]; ok {
		if state, ok := parts[partID]; ok && state != nil {
			fn(state)
		}
	}
}

// readPartState calls fn with the part state for the given session and part if
// it exists. Returns the zero value of T if the part state is not found.
func readPartState[T any](inst *openCodeInstance, sessionID, partID string, fn func(ps *openCodePartState) T) T {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if inst.seenPart == nil {
		var zero T
		return zero
	}
	parts, ok := inst.seenPart[sessionID]
	if !ok {
		var zero T
		return zero
	}
	state, ok := parts[partID]
	if !ok || state == nil {
		var zero T
		return zero
	}
	return fn(state)
}

func (inst *openCodeInstance) partState(sessionID, partID string) *openCodePartState {
	return readPartState(inst, sessionID, partID, func(ps *openCodePartState) *openCodePartState { return ps })
}

func (inst *openCodeInstance) partFlags(sessionID, partID string) (bool, bool) {
	type pair struct{ a, b bool }
	p := readPartState(inst, sessionID, partID, func(ps *openCodePartState) pair {
		return pair{ps.callSent, ps.resultSent}
	})
	return p.a, p.b
}

func (inst *openCodeInstance) partStreamFlags(sessionID, partID string) (bool, bool, bool, bool) {
	type quad struct{ a, b, c, d bool }
	q := readPartState(inst, sessionID, partID, func(ps *openCodePartState) quad {
		return quad{ps.streamInputStarted, ps.streamInputAvailable, ps.streamOutputAvailable, ps.streamOutputError}
	})
	return q.a, q.b, q.c, q.d
}

func (inst *openCodeInstance) partTextStreamFlags(sessionID, partID string) (bool, bool, bool, bool) {
	type quad struct{ a, b, c, d bool }
	q := readPartState(inst, sessionID, partID, func(ps *openCodePartState) quad {
		return quad{ps.textStreamStarted, ps.textStreamEnded, ps.reasoningStreamStarted, ps.reasoningStreamEnded}
	})
	return q.a, q.b, q.c, q.d
}

func (inst *openCodeInstance) partCallStatus(sessionID, partID string) string {
	return readPartState(inst, sessionID, partID, func(ps *openCodePartState) string { return ps.callStatus })
}

func (inst *openCodeInstance) partStatusReaction(sessionID, partID string) string {
	return readPartState(inst, sessionID, partID, func(ps *openCodePartState) string { return ps.statusReaction })
}

func (inst *openCodeInstance) setPartCallSent(sessionID, partID string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) { ps.callSent = true })
}

func (inst *openCodeInstance) setPartTextStreamStarted(sessionID, partID, kind string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) {
		if kind == "reasoning" {
			ps.reasoningStreamStarted = true
		} else {
			ps.textStreamStarted = true
		}
	})
}

func (inst *openCodeInstance) setPartTextStreamEnded(sessionID, partID, kind string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) {
		if kind == "reasoning" {
			ps.reasoningStreamEnded = true
		} else {
			ps.textStreamEnded = true
		}
	})
}

func (inst *openCodeInstance) setPartStreamInputStarted(sessionID, partID string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) { ps.streamInputStarted = true })
}

func (inst *openCodeInstance) setPartStreamInputAvailable(sessionID, partID string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) { ps.streamInputAvailable = true })
}

func (inst *openCodeInstance) setPartStreamOutputAvailable(sessionID, partID string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) { ps.streamOutputAvailable = true })
}

func (inst *openCodeInstance) setPartStreamOutputError(sessionID, partID string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) { ps.streamOutputError = true })
}

func (inst *openCodeInstance) setPartCallStatus(sessionID, partID, status string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) { ps.callStatus = status })
}

func (inst *openCodeInstance) setPartStatusReaction(sessionID, partID, reaction string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) { ps.statusReaction = reaction })
}

func (inst *openCodeInstance) setPartResultSent(sessionID, partID string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) { ps.resultSent = true })
}

func (inst *openCodeInstance) ensurePartState(sessionID, messageID, partID, role, partType string) *openCodePartState {
	if sessionID == "" || partID == "" {
		return nil
	}
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if inst.seenPart == nil {
		inst.seenPart = make(map[string]map[string]*openCodePartState)
	}
	parts, ok := inst.seenPart[sessionID]
	if !ok {
		parts = make(map[string]*openCodePartState)
		inst.seenPart[sessionID] = parts
	}
	state, ok := parts[partID]
	if !ok {
		state = &openCodePartState{role: role, messageID: messageID, partType: partType}
		parts[partID] = state
	} else {
		if role != "" {
			state.role = role
		}
		if messageID != "" {
			state.messageID = messageID
		}
		if partType != "" {
			state.partType = partType
		}
	}
	if messageID != "" {
		if inst.partsByMessage == nil {
			inst.partsByMessage = make(map[string]map[string]map[string]struct{})
		}
		msgMap, ok := inst.partsByMessage[sessionID]
		if !ok {
			msgMap = make(map[string]map[string]struct{})
			inst.partsByMessage[sessionID] = msgMap
		}
		partSet, ok := msgMap[messageID]
		if !ok {
			partSet = make(map[string]struct{})
			msgMap[messageID] = partSet
		}
		partSet[partID] = struct{}{}
	}
	return state
}

func (inst *openCodeInstance) messageParts(sessionID, messageID string) map[string]*openCodePartState {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	result := make(map[string]*openCodePartState)
	if inst.partsByMessage == nil || inst.seenPart == nil {
		return result
	}
	msgMap, ok := inst.partsByMessage[sessionID]
	if !ok {
		return result
	}
	partSet, ok := msgMap[messageID]
	if !ok {
		return result
	}
	for partID := range partSet {
		if state, ok := inst.seenPart[sessionID][partID]; ok {
			result[partID] = state
		} else {
			result[partID] = &openCodePartState{}
		}
	}
	return result
}

func (inst *openCodeInstance) removePart(sessionID, messageID, partID string) {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if inst.seenPart != nil {
		if parts, ok := inst.seenPart[sessionID]; ok {
			delete(parts, partID)
		}
	}
	if inst.partsByMessage != nil {
		if msgMap, ok := inst.partsByMessage[sessionID]; ok {
			if partSet, ok := msgMap[messageID]; ok {
				delete(partSet, partID)
				if len(partSet) == 0 {
					delete(msgMap, messageID)
				}
			}
			if len(msgMap) == 0 {
				delete(inst.partsByMessage, sessionID)
			}
		}
	}
}

func (inst *openCodeInstance) ensureTurnState(sessionID, messageID string) *openCodeTurnState {
	if sessionID == "" || messageID == "" {
		return nil
	}
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if inst.turnState == nil {
		inst.turnState = make(map[string]map[string]*openCodeTurnState)
	}
	sess, ok := inst.turnState[sessionID]
	if !ok {
		sess = make(map[string]*openCodeTurnState)
		inst.turnState[sessionID] = sess
	}
	state, ok := sess[messageID]
	if !ok {
		state = &openCodeTurnState{}
		sess[messageID] = state
	}
	return state
}

func (inst *openCodeInstance) turnStateFor(sessionID, messageID string) *openCodeTurnState {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if inst.turnState == nil {
		return nil
	}
	sess, ok := inst.turnState[sessionID]
	if !ok {
		return nil
	}
	return sess[messageID]
}

func (inst *openCodeInstance) removeTurnState(sessionID, messageID string) {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if inst.turnState == nil {
		return
	}
	sess, ok := inst.turnState[sessionID]
	if !ok {
		return
	}
	delete(sess, messageID)
	if len(sess) == 0 {
		delete(inst.turnState, sessionID)
	}
}

func opencodeMessageIDForEvent(eventID id.EventID) string {
	trimmed := strings.TrimSpace(string(eventID))
	if trimmed == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(trimmed))
	return "msg_mx_" + hex.EncodeToString(hash[:8])
}

func findOpenCodePart(parts []opencode.Part, partID string) (opencode.Part, bool) {
	if partID == "" {
		return opencode.Part{}, false
	}
	for _, part := range parts {
		if part.ID == partID {
			return part, true
		}
	}
	return opencode.Part{}, false
}
