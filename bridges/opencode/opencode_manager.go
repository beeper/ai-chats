package opencode

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

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/bridges/opencode/api"
)

// OpenCodeManager coordinates connections to OpenCode server instances,
// dispatches SSE events, and manages session lifecycle.
type OpenCodeManager struct {
	bridge       *Bridge
	mu           sync.RWMutex
	instances    map[string]*openCodeInstance
	approvalFlow *agentremote.ApprovalFlow[*permissionApprovalRef]
}

type permissionApprovalRef struct {
	RoomID       id.RoomID
	InstanceID   string
	SessionID    string
	MessageID    string
	ToolCallID   string
	PermissionID string
	Presentation agentremote.ApprovalPromptPresentation
}

func buildOpenCodeApprovalPresentation(req api.PermissionRequest) agentremote.ApprovalPromptPresentation {
	permission := strings.TrimSpace(req.Permission)
	title := "OpenCode permission request"
	if permission != "" {
		title = "OpenCode permission request: " + permission
	}
	details := make([]agentremote.ApprovalDetail, 0, 8)
	if permission != "" {
		details = append(details, agentremote.ApprovalDetail{Label: "Permission", Value: permission})
	}
	if v := agentremote.ValueSummary(req.Patterns); v != "" {
		details = append(details, agentremote.ApprovalDetail{Label: "Patterns", Value: v})
	}
	if len(req.Metadata) > 0 {
		details = agentremote.AppendDetailsFromMap(details, "Metadata", req.Metadata, 4)
	}
	return agentremote.ApprovalPromptPresentation{
		Title:       title,
		Details:     details,
		AllowAlways: len(req.Always) > 0,
	}
}

func NewOpenCodeManager(bridge *Bridge) *OpenCodeManager {
	mgr := &OpenCodeManager{
		bridge:    bridge,
		instances: make(map[string]*openCodeInstance),
	}
	mgr.approvalFlow = agentremote.NewApprovalFlow(agentremote.ApprovalFlowConfig[*permissionApprovalRef]{
		Login: func() *bridgev2.UserLogin {
			if bridge != nil && bridge.host != nil {
				return bridge.host.GetUserLogin()
			}
			return nil
		},
		Sender: func(portal *bridgev2.Portal) bridgev2.EventSender {
			if bridge == nil || bridge.host == nil {
				return bridgev2.EventSender{}
			}
			meta := bridge.portalMeta(portal)
			return bridge.host.SenderForOpenCode(meta.InstanceID, false)
		},
		BackgroundContext: func(ctx context.Context) context.Context {
			if bridge != nil && bridge.host != nil {
				return bridge.host.BackgroundContext(ctx)
			}
			return ctx
		},
		RoomIDFromData: func(data *permissionApprovalRef) id.RoomID {
			if data == nil {
				return ""
			}
			return data.RoomID
		},
		DeliverDecision: func(ctx context.Context, portal *bridgev2.Portal, pending *agentremote.Pending[*permissionApprovalRef], decision agentremote.ApprovalDecisionPayload) error {
			ref := pending.Data
			if ref == nil {
				return agentremote.ErrApprovalUnknown
			}
			response := agentremote.DecisionToString(decision, "once", "always", "reject")
			inst, err := mgr.requireConnectedInstance(ref.InstanceID)
			if err != nil {
				return err
			}
			if err := inst.client.RespondPermission(ctx, ref.SessionID, ref.PermissionID, response); err != nil {
				if api.IsAuthError(err) {
					mgr.setConnected(inst, false)
				}
				return fmt.Errorf("respond to permission: %w", err)
			}
			return nil
		},
		SendNotice: func(ctx context.Context, portal *bridgev2.Portal, msg string) {
			if bridge != nil && bridge.host != nil {
				bridge.host.SendSystemNotice(ctx, portal, msg)
			}
		},
		IDPrefix: "opencode",
		LogKey:   "opencode_msg_id",
	})
	return mgr
}

func (m *OpenCodeManager) log() *zerolog.Logger {
	if m != nil && m.bridge != nil && m.bridge.host != nil {
		if base := m.bridge.host.Log(); base != nil {
			l := base.With().Str("component", "opencode").Logger()
			return &l
		}
	}
	l := zerolog.Nop()
	return &l
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
		if inst.process != nil {
			_ = inst.process.Close()
		}
	}
	m.instances = make(map[string]*openCodeInstance)
}

func (m *OpenCodeManager) RestoreConnections(ctx context.Context) error {
	if m == nil || m.bridge == nil || m.bridge.host == nil {
		return nil
	}
	for _, cfg := range m.bridge.host.OpenCodeInstances() {
		if cfg == nil {
			continue
		}
		if cfg.Mode == OpenCodeModeManagedLauncher {
			continue
		}
		if _, _, err := m.connectConfiguredInstance(ctx, cfg); err != nil {
			m.log().Warn().Err(err).Str("instance", cfg.ID).Msg("Failed to restore OpenCode instance")
		}
	}
	return nil
}

func (m *OpenCodeManager) Connect(ctx context.Context, baseURL, password, username string) (*openCodeInstance, int, error) {
	return m.connectConfiguredInstance(ctx, &OpenCodeInstance{
		ID:          OpenCodeInstanceID(baseURL, username),
		Mode:        OpenCodeModeRemote,
		URL:         baseURL,
		Username:    username,
		Password:    password,
		HasPassword: strings.TrimSpace(password) != "",
	})
}

func (m *OpenCodeManager) connectConfiguredInstance(ctx context.Context, cfg *OpenCodeInstance) (*openCodeInstance, int, error) {
	if m == nil || m.bridge == nil || m.bridge.host == nil {
		return nil, 0, errors.New("opencode manager unavailable")
	}
	if cfg == nil {
		return nil, 0, errors.New("instance config is required")
	}

	cfgCopy := *cfg
	if cfgCopy.Mode == "" {
		cfgCopy.Mode = OpenCodeModeRemote
	}
	if cfgCopy.Mode == OpenCodeModeManagedLauncher {
		return nil, 0, errors.New("managed launcher instances are not directly connectable")
	}

	var proc *managedOpenCodeProcess
	if cfgCopy.Mode == OpenCodeModeManaged && strings.TrimSpace(cfgCopy.WorkingDirectory) != "" {
		if strings.TrimSpace(cfgCopy.URL) != "" {
			if inst, count, err := m.connectInstanceClient(ctx, &cfgCopy, nil); err == nil {
				return inst, count, nil
			}
		}
		managedProc, err := m.spawnManagedProcess(ctx, &cfgCopy, cfgCopy.WorkingDirectory)
		if err != nil {
			return nil, 0, fmt.Errorf("spawn managed opencode: %w", err)
		}
		cfgCopy.URL = managedProc.url
		cfgCopy.Username = "opencode"
		cfgCopy.Password = ""
		cfgCopy.HasPassword = false
		proc = managedProc
	}

	if strings.TrimSpace(cfgCopy.URL) == "" {
		return nil, 0, errors.New("url is required")
	}
	user := strings.TrimSpace(cfgCopy.Username)
	if user == "" {
		user = "opencode"
	}

	normalized, err := api.NormalizeBaseURL(cfgCopy.URL)
	if err != nil {
		return nil, 0, fmt.Errorf("normalize url: %w", err)
	}
	cfgCopy.URL = normalized
	cfgCopy.Username = user
	if cfgCopy.ID == "" {
		cfgCopy.ID = OpenCodeInstanceID(normalized, user)
	}
	if cfgCopy.Mode == OpenCodeModeRemote {
		cfgCopy.Password = strings.TrimSpace(cfgCopy.Password)
		cfgCopy.HasPassword = cfgCopy.Password != ""
	}
	return m.connectInstanceClient(ctx, &cfgCopy, proc)
}

func (m *OpenCodeManager) connectInstanceClient(ctx context.Context, cfg *OpenCodeInstance, proc *managedOpenCodeProcess) (*openCodeInstance, int, error) {
	client, err := api.NewClient(cfg.URL, cfg.Username, cfg.Password)
	if err != nil {
		return nil, 0, fmt.Errorf("create client: %w", err)
	}
	sessions, err := client.ListSessions(ctx)
	if err != nil {
		if proc != nil {
			_ = proc.Close()
		}
		return nil, 0, fmt.Errorf("list sessions: %w", err)
	}

	inst := &openCodeInstance{
		cfg:            *cfg,
		password:       strings.TrimSpace(cfg.Password),
		client:         client,
		process:        proc,
		connected:      true,
		seenMsg:        make(map[string]map[string]string),
		seenPart:       make(map[string]map[string]*openCodePartState),
		partsByMessage: make(map[string]map[string]map[string]struct{}),
		turnState:      make(map[string]map[string]*openCodeTurnState),
		sendQueue:      make(map[string]*openCodeSessionQueue),
	}

	m.mu.Lock()
	if existing := m.instances[cfg.ID]; existing != nil {
		existing.cancelAndStopTimer()
		if existing.process != nil {
			_ = existing.process.Close()
		}
	}
	m.instances[cfg.ID] = inst
	m.mu.Unlock()

	m.persistInstance(ctx, inst)
	m.bridge.EnsureGhostDisplayName(ctx, cfg.ID)

	count, syncErr := m.syncSessions(ctx, inst, sessions)
	m.startEventLoop(inst)
	return inst, count, syncErr
}

func (m *OpenCodeManager) persistInstance(ctx context.Context, inst *openCodeInstance) {
	meta := m.bridge.host.OpenCodeInstances()
	if meta == nil {
		meta = make(map[string]*OpenCodeInstance)
	}
	cfgCopy := inst.cfg
	cfgCopy.Password = strings.TrimSpace(inst.password)
	meta[inst.cfg.ID] = &cfgCopy
	if err := m.bridge.host.SaveOpenCodeInstances(ctx, meta); err != nil {
		m.log().Warn().Err(err).Msg("Failed to persist OpenCode instance")
	}
}

func (m *OpenCodeManager) EnsureManagedInstance(ctx context.Context, launcherID, workingDir string) (*openCodeInstance, error) {
	if m == nil || m.bridge == nil || m.bridge.host == nil {
		return nil, errors.New("opencode manager unavailable")
	}
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		return nil, errors.New("working directory is required")
	}
	all := m.bridge.host.OpenCodeInstances()
	launcher := all[launcherID]
	if launcher == nil || launcher.Mode != OpenCodeModeManagedLauncher {
		return nil, errors.New("managed launcher not found")
	}
	login := m.bridge.host.GetUserLogin()
	if login == nil {
		return nil, errors.New("login unavailable")
	}
	instanceID := OpenCodeManagedInstanceID(string(login.ID), workingDir)
	if inst := m.getInstance(instanceID); inst != nil && inst.connected {
		return inst, nil
	}
	cfg := all[instanceID]
	if cfg == nil {
		cfg = &OpenCodeInstance{
			ID:               instanceID,
			Mode:             OpenCodeModeManaged,
			BinaryPath:       launcher.BinaryPath,
			DefaultDirectory: launcher.DefaultDirectory,
			WorkingDirectory: workingDir,
			LauncherID:       launcherID,
		}
	} else {
		cfg.Mode = OpenCodeModeManaged
		cfg.BinaryPath = launcher.BinaryPath
		cfg.DefaultDirectory = launcher.DefaultDirectory
		cfg.WorkingDirectory = workingDir
		cfg.LauncherID = launcherID
	}
	inst, _, err := m.connectConfiguredInstance(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return inst, nil
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

func (m *OpenCodeManager) SendMessage(ctx context.Context, instanceID, sessionID string, parts []api.PartInput, eventID id.EventID) error {
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
		if api.IsAuthError(err) {
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
		if api.IsAuthError(err) {
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
	run func(*openCodeInstance) (*api.Session, error),
) (*api.Session, error) {
	inst, err := m.requireConnectedInstance(instanceID)
	if err != nil {
		return nil, err
	}
	session, err := run(inst)
	if err != nil {
		if api.IsAuthError(err) {
			m.setConnected(inst, false)
		}
		return nil, fmt.Errorf("%s: %w", action, err)
	}
	return session, nil
}

func (m *OpenCodeManager) CreateSession(ctx context.Context, instanceID, title, directory string) (*api.Session, error) {
	return m.runSessionMutation(ctx, instanceID, "create session", func(inst *openCodeInstance) (*api.Session, error) {
		return inst.client.CreateSession(ctx, title, directory)
	})
}

func (m *OpenCodeManager) UpdateSessionTitle(ctx context.Context, instanceID, sessionID, title string) (*api.Session, error) {
	return m.runSessionMutation(ctx, instanceID, "update session title", func(inst *openCodeInstance) (*api.Session, error) {
		return inst.client.UpdateSessionTitle(ctx, sessionID, title)
	})
}

func (m *OpenCodeManager) syncSessions(ctx context.Context, inst *openCodeInstance, sessions []api.Session) (int, error) {
	count := 0
	for _, session := range sessions {
		hadRoom := false
		if portal := m.bridge.findOpenCodePortal(ctx, inst.cfg.ID, session.ID); portal != nil && portal.MXID != "" {
			hadRoom = true
		}
		if err := m.bridge.ensureOpenCodeSessionPortal(ctx, inst, session); err != nil {
			m.log().Warn().Err(err).Str("session", session.ID).Msg("Failed to sync OpenCode session")
			continue
		}
		if hadRoom {
			m.bridge.queueOpenCodeSessionResync(inst.cfg.ID, session)
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
	login := m.bridge.host.GetUserLogin()
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
func (m *OpenCodeManager) consumeEventStream(ctx context.Context, inst *openCodeInstance, events <-chan api.Event, errs <-chan error) bool {
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

func (m *OpenCodeManager) handleEvent(ctx context.Context, inst *openCodeInstance, evt api.Event) {
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

func (m *OpenCodeManager) handleSessionEvent(ctx context.Context, inst *openCodeInstance, evt api.Event) {
	var session api.Session
	if err := evt.DecodeInfo(&session); err != nil {
		m.log().Warn().Err(err).Msg("Failed to decode session event")
		return
	}
	hadRoom := false
	if portal := m.bridge.findOpenCodePortal(ctx, inst.cfg.ID, session.ID); portal != nil && portal.MXID != "" {
		hadRoom = true
	}
	if err := m.bridge.ensureOpenCodeSessionPortal(ctx, inst, session); err != nil {
		m.log().Warn().Err(err).Str("session", session.ID).Msg("Failed to ensure session portal")
		return
	}
	if hadRoom {
		m.bridge.queueOpenCodeSessionResync(inst.cfg.ID, session)
	}
}

func (m *OpenCodeManager) handleSessionDeleted(ctx context.Context, inst *openCodeInstance, evt api.Event) {
	var session api.Session
	if err := evt.DecodeInfo(&session); err != nil {
		m.log().Warn().Err(err).Msg("Failed to decode session delete event")
		return
	}
	m.bridge.removeOpenCodeSessionPortal(ctx, inst.cfg.ID, session.ID, "opencode session deleted")
}

func (m *OpenCodeManager) handleSessionStatusEvent(ctx context.Context, inst *openCodeInstance, evt api.Event) {
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

func (m *OpenCodeManager) handleSessionIdleEvent(ctx context.Context, inst *openCodeInstance, evt api.Event) {
	var payload struct {
		SessionID string `json:"sessionID"`
	}
	if err := json.Unmarshal(evt.Properties, &payload); err != nil {
		m.log().Warn().Err(err).Msg("Failed to decode session idle event")
		return
	}
	m.processNextQueued(ctx, inst, payload.SessionID)
}

func (m *OpenCodeManager) handleMessageUpdated(ctx context.Context, inst *openCodeInstance, evt api.Event) {
	var msg api.Message
	if err := evt.DecodeInfo(&msg); err != nil {
		m.log().Warn().Err(err).Msg("Failed to decode message event")
		return
	}
	m.handleMessageEvent(ctx, inst, msg)
}

func (m *OpenCodeManager) handleMessageRemovedEvent(ctx context.Context, inst *openCodeInstance, evt api.Event) {
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

func (m *OpenCodeManager) handlePartUpdatedEvent(ctx context.Context, inst *openCodeInstance, evt api.Event) {
	var payload struct {
		Part  api.Part `json:"part"`
		Delta string   `json:"delta"`
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

func (m *OpenCodeManager) handlePartDeltaEvent(ctx context.Context, inst *openCodeInstance, evt api.Event) {
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

func (m *OpenCodeManager) handlePartRemovedEvent(ctx context.Context, inst *openCodeInstance, evt api.Event) {
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

func (m *OpenCodeManager) handlePermissionAskedEvent(ctx context.Context, inst *openCodeInstance, evt api.Event) {
	var req api.PermissionRequest
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
	approvalID := strings.TrimSpace(req.ID)
	toolCallID := approvalID
	messageID := ""
	if req.Tool != nil {
		if callID := strings.TrimSpace(req.Tool.CallID); callID != "" {
			toolCallID = callID
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
	presentation := buildOpenCodeApprovalPresentation(req)
	_, created := m.approvalFlow.Register(approvalID, 10*time.Minute, &permissionApprovalRef{
		RoomID:       portal.MXID,
		InstanceID:   inst.cfg.ID,
		SessionID:    req.SessionID,
		MessageID:    messageID,
		ToolCallID:   toolCallID,
		PermissionID: approvalID,
		Presentation: presentation,
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
	ownerMXID := id.UserID("")
	if m.bridge != nil && m.bridge.host != nil {
		if login := m.bridge.host.GetUserLogin(); login != nil {
			ownerMXID = login.UserMXID
		}
	}
	m.approvalFlow.SendPrompt(ctx, portal, agentremote.SendPromptParams{
		ApprovalPromptMessageParams: agentremote.ApprovalPromptMessageParams{
			ApprovalID:   approvalID,
			ToolCallID:   toolCallID,
			ToolName:     toolName,
			TurnID:       turnID,
			Presentation: presentation,
			ExpiresAt:    time.Now().Add(10 * time.Minute),
		},
		RoomID:    portal.MXID,
		OwnerMXID: ownerMXID,
	})
}

func (m *OpenCodeManager) handlePermissionRepliedEvent(ctx context.Context, inst *openCodeInstance, evt api.Event) {
	var payload struct {
		SessionID string `json:"sessionID"`
		RequestID string `json:"requestID"`
		Reply     string `json:"reply"`
	}
	if err := json.Unmarshal(evt.Properties, &payload); err != nil {
		m.log().Warn().Err(err).Msg("Failed to decode permission reply event")
		return
	}
	requestID := strings.TrimSpace(payload.RequestID)
	pending := m.approvalFlow.Get(requestID)
	if pending == nil {
		return
	}
	ref := pending.Data
	if ref == nil {
		m.approvalFlow.Drop(requestID)
		return
	}
	reply := strings.ToLower(strings.TrimSpace(payload.Reply))
	approved := reply != "reject"
	turnID := opencodeMessageStreamTurnID(ref.SessionID, ref.MessageID)
	portal := m.bridge.findOpenCodePortal(ctx, inst.cfg.ID, ref.SessionID)
	if portal != nil {
		m.ensureStepStarted(ctx, inst, portal, ref.SessionID, ref.MessageID)
		m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, m.bridge.portalAgentID(portal), map[string]any{
			"type":       "tool-approval-response",
			"approvalId": requestID,
			"toolCallId": ref.ToolCallID,
			"approved":   approved,
			"reason":     reply,
		})
		if !approved {
			m.bridge.emitOpenCodeStreamEvent(ctx, portal, turnID, m.bridge.portalAgentID(portal), map[string]any{
				"type":       "tool-output-denied",
				"toolCallId": ref.ToolCallID,
			})
		}
	}
	m.approvalFlow.ResolveExternal(ctx, requestID, agentremote.ApprovalDecisionPayload{
		ApprovalID: requestID,
		Approved:   approved,
		Always:     reply == "always",
		Reason:     reply,
	})
}

func (m *OpenCodeManager) handleQuestionAskedEvent(ctx context.Context, inst *openCodeInstance, evt api.Event) {
	var req api.QuestionRequest
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

// ---------- message/part processing ----------

func (m *OpenCodeManager) handleMessageEvent(ctx context.Context, inst *openCodeInstance, msg api.Message) {
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

func (m *OpenCodeManager) handleMessageParts(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, role string, msg *api.MessageWithParts) {
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
		fillPartIDs(&part, msg.Info.ID, msg.Info.SessionID)
		m.syncAssistantMessagePart(ctx, inst, portal, msg, part)
		m.handlePart(ctx, inst, portal, role, part, false)
	}
}

func (m *OpenCodeManager) handlePartUpdated(ctx context.Context, inst *openCodeInstance, part api.Part, delta string) {
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
	if delta != "" {
		switch part.Type {
		case "tool":
			m.emitToolStreamDelta(ctx, inst, portal, part, delta)
		case "text":
			m.emitTextStreamDeltaForKind(ctx, inst, portal, part, delta, "text")
		case "reasoning":
			m.emitTextStreamDeltaForKind(ctx, inst, portal, part, delta, "reasoning")
		}
	}
	m.emitTextStreamEnd(ctx, inst, portal, part)
	m.handlePart(ctx, inst, portal, role, part, true)
}

// resolvePartRole determines the role for a part, fetching the full message if needed.
func (m *OpenCodeManager) resolvePartRole(ctx context.Context, inst *openCodeInstance, part api.Part) string {
	role := inst.seenRole(part.SessionID, part.MessageID)
	if role == "user" {
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
		return "assistant"
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

	part := api.Part{
		ID:        partID,
		SessionID: sessionID,
		MessageID: messageID,
		Type:      field,
	}
	inst.ensurePartState(sessionID, messageID, partID, role, field)

	switch field {
	case "text", "reasoning":
		m.emitTextStreamDeltaForKind(ctx, inst, portal, part, delta, field)
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

func (m *OpenCodeManager) handlePart(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, role string, part api.Part, allowEdit bool) {
	if part.ID == "" || part.SessionID == "" {
		return
	}
	if part.Type == "tool" {
		m.handleToolPart(ctx, inst, portal, role, part)
		return
	}

	isNew := inst.partState(part.SessionID, part.ID) == nil
	if isNew {
		inst.ensurePartState(part.SessionID, part.MessageID, part.ID, role, part.Type)
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

	// User-owned part handling.
	if isNew {
		m.bridge.emitOpenCodePartEvent(portal, inst.cfg.ID, part, true, bridgev2.RemoteEventMessage)
		return
	}
	if allowEdit && (part.Type == "text" || part.Type == "reasoning") {
		m.bridge.emitOpenCodePartEvent(portal, inst.cfg.ID, part, true, bridgev2.RemoteEventEdit)
	}
	if part.Type == "text" || part.Type == "reasoning" {
		m.emitTextStreamEnd(ctx, inst, portal, part)
	}
}

func (m *OpenCodeManager) handleToolPart(ctx context.Context, inst *openCodeInstance, portal *bridgev2.Portal, role string, part api.Part) {
	state := inst.ensurePartState(part.SessionID, part.MessageID, part.ID, role, part.Type)
	if state == nil {
		return
	}
	var status string
	if part.State != nil {
		status = part.State.Status
	}
	m.emitToolStreamState(ctx, inst, portal, part)
	callSent, resultSent := inst.partFlags(part.SessionID, part.ID)
	callStatus := inst.partCallStatus(part.SessionID, part.ID)
	if !callSent && status != "" {
		inst.withPartState(part.SessionID, part.ID, func(ps *openCodePartState) {
			ps.callSent = true
			ps.callStatus = status
		})
	} else if callSent && status != "" && status != callStatus {
		inst.withPartState(part.SessionID, part.ID, func(ps *openCodePartState) { ps.callStatus = status })
	}
	if !resultSent && (status == "completed" || status == "error") {
		inst.withPartState(part.SessionID, part.ID, func(ps *openCodePartState) { ps.resultSent = true })
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
	login := m.bridge.host.GetUserLogin()
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

func findOpenCodePart(parts []api.Part, partID string) (api.Part, bool) {
	for _, part := range parts {
		if part.ID == partID {
			return part, true
		}
	}
	return api.Part{}, false
}
