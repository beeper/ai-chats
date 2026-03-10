package openclaw

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/pkg/bridgeadapter"
	"github.com/beeper/agentremote/pkg/connector/msgconv"
	"github.com/beeper/agentremote/pkg/matrixevents"
	"github.com/beeper/agentremote/pkg/shared/jsonutil"
	"github.com/beeper/agentremote/pkg/shared/openclawconv"
	"github.com/beeper/agentremote/pkg/shared/streamui"
)

type openClawManager struct {
	client *OpenClawClient

	mu        sync.RWMutex
	gateway   *gatewayWSClient
	sessions  map[string]gatewaySessionRow
	approvals *bridgeadapter.ApprovalManager[*openClawPendingApprovalData]
	waiting   map[string]struct{}
	started   map[string]struct{}
	resyncing map[string]time.Time

	cancel context.CancelFunc
}

type openClawPendingApprovalData struct {
	SessionKey  string
	TurnID      string
	ToolCallID  string
	ToolName    string
	Command     string
	Recovered   bool
	CreatedAtMs int64
	ExpiresAtMs int64
}

func newOpenClawManager(client *OpenClawClient) *openClawManager {
	return &openClawManager{
		client:    client,
		sessions:  make(map[string]gatewaySessionRow),
		approvals: bridgeadapter.NewApprovalManager[*openClawPendingApprovalData](),
		waiting:   make(map[string]struct{}),
		started:   make(map[string]struct{}),
		resyncing: make(map[string]time.Time),
	}
}

func (m *openClawManager) Start(ctx context.Context) (bool, error) {
	meta := loginMetadata(m.client.UserLogin)
	cfg := gatewayConnectConfig{
		URL:         meta.GatewayURL,
		Token:       meta.GatewayToken,
		Password:    meta.GatewayPassword,
		DeviceToken: meta.DeviceToken,
	}
	gw := newGatewayWSClient(cfg)
	deviceToken, err := gw.Connect(ctx)
	if err != nil {
		return false, err
	}
	if deviceToken != "" && deviceToken != meta.DeviceToken {
		meta.DeviceToken = deviceToken
		_ = m.client.UserLogin.Save(ctx)
	}
	runCtx, cancel := context.WithCancel(ctx)
	started := false
	defer func() {
		cancel()
		if !started || ctx.Err() == nil {
			gw.Close()
		}
		m.mu.Lock()
		if m.gateway == gw {
			m.gateway = nil
		}
		m.cancel = nil
		m.started = make(map[string]struct{})
		m.resyncing = make(map[string]time.Time)
		m.mu.Unlock()
	}()
	m.mu.Lock()
	m.gateway = gw
	m.cancel = cancel
	m.mu.Unlock()
	if err = m.syncSessions(ctx); err != nil {
		return false, err
	}
	if _, err := m.client.loadAgentCatalog(m.client.BackgroundContext(ctx), true); err != nil {
		m.client.Log().Debug().Err(err).Msg("Failed to refresh OpenClaw agent catalog on connect")
	}
	if _, err := m.client.loadModelCatalog(m.client.BackgroundContext(ctx), true); err != nil {
		m.client.Log().Debug().Err(err).Msg("Failed to refresh OpenClaw model catalog on connect")
	}
	m.client.loggedIn.Store(true)
	m.client.UserLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected, Message: "Connected"})
	started = true
	m.eventLoop(runCtx, gw.Events())
	if ctx.Err() != nil {
		return true, nil
	}
	if err := gw.LastError(); err != nil {
		return true, err
	}
	return true, errors.New("gateway connection closed")
}

func (m *openClawManager) Stop() {
	m.mu.Lock()
	cancel := m.cancel
	gateway := m.gateway
	m.cancel = nil
	m.gateway = nil
	m.started = make(map[string]struct{})
	m.resyncing = make(map[string]time.Time)
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if gateway != nil {
		gateway.Close()
	}
}

func (m *openClawManager) syncSessions(ctx context.Context) error {
	gateway := m.gatewayClient()
	if gateway == nil {
		return errors.New("gateway client is unavailable")
	}
	sessions, err := gateway.ListSessions(ctx, openClawDefaultSessionLimit)
	if err != nil {
		return err
	}
	m.mu.Lock()
	for _, session := range sessions {
		m.sessions[session.Key] = session
		delete(m.resyncing, session.Key)
	}
	m.mu.Unlock()
	for _, session := range sessions {
		m.client.UserLogin.QueueRemoteEvent(&OpenClawSessionResyncEvent{client: m.client, session: session})
	}
	meta := loginMetadata(m.client.UserLogin)
	meta.SessionsSynced = true
	meta.LastSyncAt = time.Now().UnixMilli()
	return m.client.UserLogin.Save(ctx)
}

func (m *openClawManager) gatewayClient() *gatewayWSClient {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.gateway
}

func (m *openClawManager) discoveredAgentIDs() []string {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.sessions) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(m.sessions))
	agentIDs := make([]string, 0, len(m.sessions))
	for _, session := range m.sessions {
		agentID := strings.TrimSpace(openClawAgentIDFromSessionKey(session.Key))
		if agentID == "" {
			continue
		}
		key := strings.ToLower(agentID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)
	return agentIDs
}

func (m *openClawManager) requireGateway() (*gatewayWSClient, error) {
	gateway := m.gatewayClient()
	if gateway == nil {
		return nil, errors.New("gateway client is unavailable")
	}
	return gateway, nil
}

func (m *openClawManager) trackWaitingRun(runID string) bool {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.waiting[runID]; exists {
		return false
	}
	m.waiting[runID] = struct{}{}
	return true
}

func (m *openClawManager) untrackWaitingRun(runID string) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return
	}
	m.mu.Lock()
	delete(m.waiting, runID)
	m.mu.Unlock()
}

func (m *openClawManager) forgetSession(sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	m.mu.Lock()
	delete(m.sessions, sessionKey)
	delete(m.resyncing, sessionKey)
	m.mu.Unlock()
}

func (m *openClawManager) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	gateway, err := m.requireGateway()
	if err != nil {
		return nil, err
	}
	meta := portalMeta(msg.Portal)
	body := strings.TrimSpace(msg.Content.Body)
	if isOpenClawAbortCommand(body, msg.Content.MsgType, msg.Event.Type) {
		if err := gateway.AbortRun(ctx, meta.OpenClawSessionKey, ""); err != nil {
			return nil, err
		}
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	if handled, err := m.handleControlCommand(ctx, msg, gateway, body); handled || err != nil {
		return &bridgev2.MatrixMessageResponse{Pending: false}, err
	}

	attachments, text, err := m.buildOutboundPayload(ctx, msg)
	if err != nil {
		return nil, err
	}
	if text == "" && len(attachments) == 0 {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	if meta.OpenClawDMCreatedFromContact && meta.OpenClawSessionID == "" && isOpenClawSyntheticDMSessionKey(meta.OpenClawSessionKey) {
		if resolvedKey, err := gateway.ResolveSessionKey(ctx, meta.OpenClawSessionKey); err == nil && strings.TrimSpace(resolvedKey) != "" && strings.TrimSpace(resolvedKey) != strings.TrimSpace(meta.OpenClawSessionKey) {
			meta.OpenClawSessionKey = strings.TrimSpace(resolvedKey)
			if saveErr := msg.Portal.Save(ctx); saveErr != nil {
				m.client.Log().Warn().Err(saveErr).Str("portal_key", string(msg.Portal.PortalKey.ID)).Msg("Failed to save OpenClaw portal after resolved session key update")
			}
		}
	}
	_, err = gateway.SendMessage(
		ctx,
		meta.OpenClawSessionKey,
		text,
		attachments,
		meta.ThinkingLevel,
		meta.VerboseLevel,
		string(msg.Event.ID),
	)
	if err != nil {
		return nil, err
	}
	if meta.OpenClawDMCreatedFromContact && meta.OpenClawSessionID == "" && isOpenClawSyntheticDMSessionKey(meta.OpenClawSessionKey) {
		go m.syncSessions(m.client.BackgroundContext(ctx))
	}
	return &bridgev2.MatrixMessageResponse{Pending: true}, nil
}

func (m *openClawManager) buildOutboundPayload(ctx context.Context, msg *bridgev2.MatrixMessage) ([]map[string]any, string, error) {
	content := msg.Content
	msgType := content.MsgType
	if msg.Event.Type == event.EventSticker {
		msgType = event.MsgImage
	}
	switch msgType {
	case event.MsgText, event.MsgNotice, event.MsgEmote:
		return nil, strings.TrimSpace(content.Body), nil
	case event.MsgImage, event.MsgVideo, event.MsgAudio, event.MsgFile:
		mediaURL := string(content.URL)
		if mediaURL == "" && content.File != nil {
			mediaURL = string(content.File.URL)
		}
		if mediaURL == "" {
			return nil, "", errors.New("missing media URL")
		}
		encoded, mimeType, err := m.client.DownloadAndEncodeMedia(ctx, mediaURL, content.File, 50)
		if err != nil {
			return nil, "", err
		}
		if content.Info != nil && strings.TrimSpace(content.Info.MimeType) != "" {
			mimeType = strings.TrimSpace(content.Info.MimeType)
		}
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		fileName := strings.TrimSpace(content.FileName)
		if fileName == "" {
			exts, _ := mime.ExtensionsByType(mimeType)
			if len(exts) > 0 {
				fileName = "file" + exts[0]
			} else {
				fileName = "file"
			}
		}
		text := strings.TrimSpace(content.Body)
		if text == fileName {
			text = ""
		}
		return []map[string]any{{
			"type":     "file",
			"mimeType": mimeType,
			"fileName": fileName,
			"content":  encoded,
		}}, text, nil
	default:
		return nil, "", fmt.Errorf("unsupported message type %s", msgType)
	}
}

func isOpenClawAbortCommand(body string, msgType event.MessageType, evtType event.Type) bool {
	if evtType == event.EventSticker || msgType == event.MsgImage || msgType == event.MsgVideo || msgType == event.MsgAudio || msgType == event.MsgFile {
		return false
	}
	body = strings.ToLower(strings.TrimSpace(body))
	switch body {
	case "stop", "/stop", "stop run", "stop action", "please stop", "stop openclaw":
		return true
	default:
		return false
	}
}

type openClawControlCommand struct {
	Action string
	Value  string
	Clear  bool
}

func parseOpenClawControlCommand(body string, msgType event.MessageType, evtType event.Type) (*openClawControlCommand, bool) {
	if evtType == event.EventSticker || msgType == event.MsgImage || msgType == event.MsgVideo || msgType == event.MsgAudio || msgType == event.MsgFile {
		return nil, false
	}
	body = strings.TrimSpace(body)
	if !strings.HasPrefix(body, "/") {
		return nil, false
	}
	fields := strings.Fields(body)
	if len(fields) == 0 {
		return nil, false
	}
	cmd := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
	rest := strings.TrimSpace(strings.TrimPrefix(body, fields[0]))
	switch cmd {
	case "reset":
		if rest != "" {
			return nil, false
		}
		return &openClawControlCommand{Action: "reset"}, true
	case "rename", "label":
		if rest == "" {
			return nil, false
		}
		if strings.EqualFold(rest, "clear") || rest == "-" {
			return &openClawControlCommand{Action: "label", Clear: true}, true
		}
		return &openClawControlCommand{Action: "label", Value: rest}, true
	case "thinking", "verbose", "reasoning":
		if rest == "" {
			return nil, false
		}
		value := strings.ToLower(strings.TrimSpace(rest))
		if value == "inherit" || value == "default" || value == "-" {
			return &openClawControlCommand{Action: cmd, Clear: true}, true
		}
		return &openClawControlCommand{Action: cmd, Value: value}, true
	default:
		return nil, false
	}
}

func (m *openClawManager) handleControlCommand(ctx context.Context, msg *bridgev2.MatrixMessage, gateway *gatewayWSClient, body string) (bool, error) {
	if msg == nil || msg.Portal == nil || gateway == nil {
		return false, nil
	}
	command, ok := parseOpenClawControlCommand(body, msg.Content.MsgType, msg.Event.Type)
	if !ok {
		return false, nil
	}
	meta := portalMeta(msg.Portal)
	sessionKey := strings.TrimSpace(meta.OpenClawSessionKey)
	if sessionKey == "" {
		m.client.sendSystemNoticeViaPortal(ctx, msg.Portal, "OpenClaw session key is unavailable for this room.")
		return true, nil
	}
	switch command.Action {
	case "reset":
		if err := gateway.ResetSession(ctx, sessionKey); err != nil {
			return true, err
		}
		m.client.sendSystemNoticeViaPortal(ctx, msg.Portal, "OpenClaw session reset.")
	case "label":
		value := any(nil)
		notice := "OpenClaw session label cleared."
		if !command.Clear {
			value = command.Value
			notice = "OpenClaw session label set to " + command.Value + "."
		}
		if err := gateway.PatchSession(ctx, sessionKey, map[string]any{"label": value}); err != nil {
			return true, err
		}
		m.client.sendSystemNoticeViaPortal(ctx, msg.Portal, notice)
	case "thinking":
		value := any(nil)
		notice := "OpenClaw thinking level cleared."
		if !command.Clear {
			value = command.Value
			notice = "OpenClaw thinking level set to " + command.Value + "."
		}
		if err := gateway.PatchSession(ctx, sessionKey, map[string]any{"thinkingLevel": value}); err != nil {
			return true, err
		}
		m.client.sendSystemNoticeViaPortal(ctx, msg.Portal, notice)
	case "verbose":
		value := any(nil)
		notice := "OpenClaw verbose level cleared."
		if !command.Clear {
			value = command.Value
			notice = "OpenClaw verbose level set to " + command.Value + "."
		}
		if err := gateway.PatchSession(ctx, sessionKey, map[string]any{"verboseLevel": value}); err != nil {
			return true, err
		}
		m.client.sendSystemNoticeViaPortal(ctx, msg.Portal, notice)
	case "reasoning":
		value := any(nil)
		notice := "OpenClaw reasoning level cleared."
		if !command.Clear {
			value = command.Value
			notice = "OpenClaw reasoning level set to " + command.Value + "."
		}
		if err := gateway.PatchSession(ctx, sessionKey, map[string]any{"reasoningLevel": value}); err != nil {
			return true, err
		}
		m.client.sendSystemNoticeViaPortal(ctx, msg.Portal, notice)
	default:
		return false, nil
	}
	if err := m.syncSessions(ctx); err != nil {
		m.client.Log().Debug().Err(err).Str("session_key", sessionKey).Msg("Failed to refresh OpenClaw sessions after control command")
	}
	return true, nil
}

func (m *openClawManager) ResolveApprovalDecision(ctx context.Context, portal *bridgev2.Portal, decision bridgeadapter.ApprovalDecisionPayload) error {
	gateway, err := m.requireGateway()
	if err != nil {
		return err
	}
	pending := m.approvals.Get(strings.TrimSpace(decision.ApprovalID))
	if pending == nil {
		return bridgeadapter.ErrApprovalUnknown
	}
	data, _ := pending.Data.(*openClawPendingApprovalData)
	if data != nil {
		if strings.TrimSpace(data.SessionKey) != strings.TrimSpace(portalMeta(portal).OpenClawSessionKey) {
			return bridgeadapter.ErrApprovalWrongRoom
		}
	}
	upstreamDecision := "deny"
	if decision.Approved {
		upstreamDecision = "allow-once"
		if decision.Always {
			upstreamDecision = "allow-always"
		}
	}
	if err := gateway.ResolveApproval(ctx, decision.ApprovalID, upstreamDecision); err != nil {
		return err
	}
	return nil
}

func (m *openClawManager) FetchMessages(ctx context.Context, params bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	gateway, err := m.requireGateway()
	if err != nil {
		return nil, err
	}
	meta := portalMeta(params.Portal)
	history, err := gateway.RecentHistory(ctx, meta.OpenClawSessionKey, normalizeHistoryLimit(params.Count))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(history.SessionID) != "" {
		meta.OpenClawSessionID = strings.TrimSpace(history.SessionID)
	}
	if strings.TrimSpace(history.ThinkingLevel) != "" {
		meta.ThinkingLevel = strings.TrimSpace(history.ThinkingLevel)
	}
	if strings.TrimSpace(history.VerboseLevel) != "" {
		meta.VerboseLevel = strings.TrimSpace(history.VerboseLevel)
	}
	messages := make([]map[string]any, 0, len(history.Messages))
	for _, message := range history.Messages {
		if message != nil {
			messages = append(messages, message)
		}
	}
	sort.SliceStable(messages, func(i, j int) bool {
		return extractMessageTimestamp(messages[i]).Before(extractMessageTimestamp(messages[j]))
	})
	backfill := make([]*bridgev2.BackfillMessage, 0, len(messages))
	for _, message := range messages {
		converted, sender, messageID := m.convertHistoryMessage(ctx, params.Portal, meta, message)
		if converted == nil || messageID == "" {
			continue
		}
		ts := extractMessageTimestamp(message)
		backfill = append(backfill, &bridgev2.BackfillMessage{
			ConvertedMessage: converted,
			Sender:           sender,
			ID:               messageID,
			TxnID:            networkid.TransactionID(messageID),
			Timestamp:        ts,
			StreamOrder:      ts.UnixMilli(),
		})
	}
	meta.LastHistorySyncAt = time.Now().UnixMilli()
	if err := params.Portal.Save(ctx); err != nil {
		m.client.Log().Warn().Err(err).Str("session_key", meta.OpenClawSessionKey).Msg("Failed saving OpenClaw portal metadata after history fetch")
	}
	return &bridgev2.FetchMessagesResponse{
		Messages:                backfill,
		HasMore:                 false,
		Forward:                 params.Forward,
		AggressiveDeduplication: true,
		ApproxTotalCount:        len(history.Messages),
	}, nil
}

func normalizeHistoryLimit(count int) int {
	if count <= 0 || count > openClawDefaultSessionLimit {
		return openClawDefaultSessionLimit
	}
	return count
}

func (m *openClawManager) convertHistoryMessage(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, message map[string]any) (*bridgev2.ConvertedMessage, bridgev2.EventSender, networkid.MessageID) {
	message = normalizeOpenClawLiveMessage(0, message)
	if len(message) == 0 {
		return nil, bridgev2.EventSender{}, ""
	}
	role := openClawMessageRole(message)
	text := extractMessageText(message)
	attachmentBlocks := extractAttachmentMetadata(message)
	if role == "toolresult" && strings.TrimSpace(text) == "" {
		if details, ok := message["details"]; ok && details != nil {
			if data, err := json.Marshal(details); err == nil {
				text = string(data)
			}
		}
	}
	agentID := resolveOpenClawAgentID(meta, meta.OpenClawSessionKey, message)
	sender := m.client.senderForAgent(agentID, false)
	if role == "user" {
		sender = m.client.senderForAgent("", true)
	}
	ts := extractMessageTimestamp(message)
	messageID := historyFingerprintMessageID(meta.OpenClawSessionKey, role, ts, text, message)
	uiParts, uiMetadata := convertHistoryToCanonicalUI(message, role, meta)
	if len(uiParts) == 0 && strings.TrimSpace(text) == "" && len(attachmentBlocks) == 0 {
		return nil, bridgev2.EventSender{}, ""
	}
	if turnID := strings.TrimSpace(stringValue(uiMetadata["turn_id"])); turnID == "" {
		uiMetadata["turn_id"] = string(messageID)
	}
	parts := make([]*bridgev2.ConvertedMessagePart, 0, 1+len(attachmentBlocks))
	if strings.TrimSpace(text) != "" {
		parts = append(parts, &bridgev2.ConvertedMessagePart{
			ID:      networkid.PartID("0"),
			Type:    event.EventMessage,
			Content: &event.MessageEventContent{MsgType: event.MsgText, Body: text},
			Extra:   map[string]any{"msgtype": event.MsgText, "body": text, "m.mentions": map[string]any{}},
		})
	} else if len(uiParts) > 0 {
		fallbackText := openClawHistoryFallbackText(uiParts)
		if fallbackText != "" {
			parts = append(parts, &bridgev2.ConvertedMessagePart{
				ID:      networkid.PartID("0"),
				Type:    event.EventMessage,
				Content: &event.MessageEventContent{MsgType: event.MsgNotice, Body: fallbackText},
				Extra:   map[string]any{"msgtype": event.MsgNotice, "body": fallbackText, "m.mentions": map[string]any{}},
			})
		}
	}
	for idx, block := range attachmentBlocks {
		uploaded, err := m.client.buildOpenClawAttachmentContent(ctx, portal, block)
		if err != nil {
			fallbackText := openClawAttachmentFallbackText(block, err)
			parts = append(parts, &bridgev2.ConvertedMessagePart{
				ID:      networkid.PartID(fmt.Sprintf("attachment-fallback-%d", idx)),
				Type:    event.EventMessage,
				Content: &event.MessageEventContent{MsgType: event.MsgNotice, Body: fallbackText},
				Extra:   map[string]any{"msgtype": event.MsgNotice, "body": fallbackText, "m.mentions": map[string]any{}},
			})
			uiParts = append(uiParts, map[string]any{"type": "text", "text": fallbackText, "state": "done"})
			continue
		}
		parts = append(parts, &bridgev2.ConvertedMessagePart{
			ID:      networkid.PartID(fmt.Sprintf("attachment-%d", idx)),
			Type:    event.EventMessage,
			Content: uploaded.Content,
			Extra:   uploaded.Metadata,
		})
		uiPart := map[string]any{
			"type":      "file",
			"mediaType": uploaded.Content.Info.MimeType,
			"filename":  uploaded.Content.FileName,
		}
		if uploaded.MatrixURL != "" {
			uiPart["url"] = uploaded.MatrixURL
		}
		uiParts = append(uiParts, uiPart)
	}
	if len(parts) == 0 {
		return nil, bridgev2.EventSender{}, ""
	}
	converted := &bridgev2.ConvertedMessage{
		Parts: parts,
	}
	if len(converted.Parts) > 0 {
		uiRole := "assistant"
		if role == "user" {
			uiRole = "user"
		}
		uiMessage := msgconv.BuildUIMessage(msgconv.UIMessageParams{
			TurnID:   string(messageID),
			Role:     uiRole,
			Metadata: uiMetadata,
			Parts:    uiParts,
		})
		converted.Parts[0].DBMetadata = buildOpenClawHistoryMessageMetadata(message, meta, role, agentID, text, attachmentBlocks, uiMetadata, uiMessage)
		converted.Parts[0].Extra[matrixevents.BeeperAIKey] = uiMessage
		converted.Parts[0].DBMetadata.(*MessageMetadata).CanonicalSchema = "ai-sdk-ui-message-v1"
		converted.Parts[0].DBMetadata.(*MessageMetadata).CanonicalUIMessage = uiMessage
	}
	return converted, sender, messageID
}

func buildOpenClawHistoryMessageMetadata(message map[string]any, meta *PortalMetadata, role, agentID, text string, attachmentBlocks []map[string]any, uiMetadata, uiMessage map[string]any) *MessageMetadata {
	metadata := &MessageMetadata{
		Role:            role,
		Body:            text,
		SessionID:       meta.OpenClawSessionID,
		SessionKey:      meta.OpenClawSessionKey,
		AgentID:         agentID,
		Attachments:     attachmentBlocks,
		ThinkingContent: openClawCanonicalReasoningText(uiMessage),
		ToolCalls:       openClawCanonicalToolCalls(uiMessage),
		GeneratedFiles:  openClawCanonicalGeneratedFiles(uiMessage),
	}
	if value := strings.TrimSpace(stringValue(uiMetadata["completion_id"])); value != "" {
		metadata.RunID = value
	}
	if value := strings.TrimSpace(stringValue(uiMetadata["turn_id"])); value != "" {
		metadata.TurnID = value
	}
	if value := strings.TrimSpace(stringValue(uiMetadata["finish_reason"])); value != "" {
		metadata.FinishReason = value
	}
	if value := strings.TrimSpace(stringValue(uiMetadata["error_text"])); value != "" {
		metadata.ErrorText = value
	}
	usage := jsonutil.ToMap(uiMetadata["usage"])
	if value, ok := openClawUsageInt64(usage, "prompt_tokens"); ok {
		metadata.PromptTokens = value
	}
	if value, ok := openClawUsageInt64(usage, "completion_tokens"); ok {
		metadata.CompletionTokens = value
	}
	if value, ok := openClawUsageInt64(usage, "reasoning_tokens"); ok {
		metadata.ReasoningTokens = value
	}
	if value, ok := openClawUsageInt64(usage, "total_tokens"); ok {
		metadata.TotalTokens = value
	}
	return metadata
}

func historyFingerprintMessageID(sessionKey, role string, ts time.Time, text string, raw map[string]any) networkid.MessageID {
	hashSource := map[string]any{
		"sessionKey":   sessionKey,
		"role":         role,
		"timestamp":    ts.UnixMilli(),
		"text":         text,
		"attachments":  extractAttachmentMetadata(raw),
		"turnId":       historyMessageTurnID(raw),
		"messageId":    openClawMessageStringField(raw, "id"),
		"messageRunId": openClawMessageStringField(raw, "runId", "run_id"),
	}
	data, _ := json.Marshal(hashSource)
	sum := sha256.Sum256(data)
	return networkid.MessageID("openclaw:" + hex.EncodeToString(sum[:12]))
}

func openClawStreamMessageMetadata(meta *PortalMetadata, payload gatewayChatEvent, agentID, turnID string) map[string]any {
	params := msgconv.UIMessageMetadataParams{
		TurnID:       turnID,
		AgentID:      agentID,
		CompletionID: payload.RunID,
		FinishReason: stringsTrimDefault(strings.TrimSpace(payload.StopReason), strings.TrimSpace(payload.State)),
		IncludeUsage: true,
	}
	if usage := normalizeOpenClawUsage(payload.Usage); len(usage) > 0 {
		if value, ok := openClawUsageInt64(usage, "prompt_tokens"); ok {
			params.PromptTokens = value
		}
		if value, ok := openClawUsageInt64(usage, "completion_tokens"); ok {
			params.CompletionTokens = value
		}
		if value, ok := openClawUsageInt64(usage, "reasoning_tokens"); ok {
			params.ReasoningTokens = value
		}
		if value, ok := openClawUsageInt64(usage, "total_tokens"); ok {
			params.TotalTokens = value
		}
	}
	metadata := msgconv.BuildUIMessageMetadata(params)
	if sessionID := stringsTrimDefault(stringValue(payload.Message["sessionId"]), meta.OpenClawSessionID); sessionID != "" {
		metadata["session_id"] = sessionID
	}
	if sessionKey := stringsTrimDefault(payload.SessionKey, meta.OpenClawSessionKey); sessionKey != "" {
		metadata["session_key"] = sessionKey
	}
	if errorText := openClawErrorText(payload); errorText != "" {
		metadata["error_text"] = errorText
	}
	return metadata
}

func normalizeOpenClawUsage(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	normalized := make(map[string]any, 3)
	if value, ok := openClawUsageNumber(raw, "prompt_tokens", "promptTokens", "inputTokens", "input_tokens", "input"); ok {
		normalized["prompt_tokens"] = int64(value)
	}
	if value, ok := openClawUsageNumber(raw, "completion_tokens", "completionTokens", "outputTokens", "output_tokens", "output"); ok {
		normalized["completion_tokens"] = int64(value)
	}
	if value, ok := openClawUsageNumber(raw, "reasoning_tokens", "reasoningTokens", "reasoning_tokens"); ok {
		normalized["reasoning_tokens"] = int64(value)
	}
	if value, ok := openClawUsageNumber(raw, "total_tokens", "totalTokens", "total"); ok {
		normalized["total_tokens"] = int64(value)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func openClawUsageNumber(raw map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		switch typed := raw[key].(type) {
		case int:
			return float64(typed), true
		case int64:
			return float64(typed), true
		case float64:
			return typed, true
		case json.Number:
			if value, err := typed.Float64(); err == nil {
				return value, true
			}
		}
	}
	return 0, false
}

func openClawUsageInt64(raw map[string]any, key string) (int64, bool) {
	value, ok := openClawUsageNumber(raw, key)
	return int64(value), ok
}

func openClawErrorText(payload gatewayChatEvent) string {
	return stringsTrimDefault(payload.ErrorMessage, stringsTrimDefault(payload.StopReason, ""))
}

func extractOpenClawEventTimestamp(eventTS int64, message map[string]any) time.Time {
	if ts := extractMessageTimestamp(message); !ts.IsZero() && !ts.Equal(openClawMissingMessageTimestamp) {
		return ts
	}
	if eventTS > 0 {
		return time.UnixMilli(eventTS)
	}
	return time.Time{}
}

func normalizeOpenClawLiveMessage(eventTS int64, message map[string]any) map[string]any {
	if len(message) == 0 {
		return nil
	}
	normalized := make(map[string]any, len(message)+1)
	for key, value := range message {
		normalized[key] = value
	}
	if nested := jsonutil.ToMap(normalized["message"]); len(nested) > 0 {
		for _, key := range []string{
			"role",
			"text",
			"content",
			"timestamp",
			"turnId",
			"turn_id",
			"runId",
			"run_id",
			"id",
			"sessionKey",
			"session_key",
			"sessionId",
			"session_id",
			"agentId",
			"agent_id",
			"agent",
			"usage",
			"model",
			"stopReason",
			"stop_reason",
			"error",
			"errorMessage",
		} {
			if _, has := normalized[key]; has {
				continue
			}
			if value, ok := nested[key]; ok {
				normalized[key] = value
			}
		}
	}
	if _, ok := normalized["timestamp"]; !ok && eventTS > 0 {
		normalized["timestamp"] = eventTS
	}
	return normalized
}

func isOpenClawDirectChatEvent(state string, message map[string]any) bool {
	if len(message) == 0 {
		return false
	}
	role := openClawMessageRole(message)
	if role == "" || role == "assistant" {
		return false
	}
	normalizedState := strings.ToLower(strings.TrimSpace(state))
	if normalizedState == "" {
		return role == "user"
	}
	switch normalizedState {
	case "final", "done", "complete", "completed":
		return true
	default:
		return role == "user"
	}
}

func openClawApprovalDecisionStatus(decision string) (bool, string) {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "allow-always":
		return true, "allow-always"
	case "deny":
		return false, "deny"
	default:
		return true, ""
	}
}

func openClawApprovalResolvedText(decision string) string {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "allow-always":
		return "Tool approval allowed always"
	case "deny":
		return "Tool approval denied"
	default:
		return "Tool approval allowed"
	}
}

func extractAttachmentMetadata(message map[string]any) []map[string]any {
	return openclawconv.ExtractAttachmentBlocks(message)
}

func (m *openClawManager) eventLoop(ctx context.Context, events <-chan gatewayEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}
			m.handleEvent(ctx, evt)
		}
	}
}

func (m *openClawManager) handleEvent(ctx context.Context, evt gatewayEvent) {
	switch evt.Name {
	case "chat":
		var payload gatewayChatEvent
		if err := json.Unmarshal(evt.Payload, &payload); err == nil {
			m.handleChatEvent(ctx, payload)
		}
	case "agent":
		var payload gatewayAgentEvent
		if err := json.Unmarshal(evt.Payload, &payload); err == nil {
			m.handleAgentEvent(ctx, payload)
		}
	case "exec.approval.requested":
		var payload gatewayApprovalRequestEvent
		if err := json.Unmarshal(evt.Payload, &payload); err == nil {
			m.handleApprovalRequest(ctx, payload)
		}
	case "exec.approval.resolved":
		var payload gatewayApprovalResolvedEvent
		if err := json.Unmarshal(evt.Payload, &payload); err == nil {
			m.handleApprovalResolved(ctx, payload)
		}
	}
}

func (m *openClawManager) handleApprovalRequest(ctx context.Context, payload gatewayApprovalRequestEvent) {
	sessionKey := strings.TrimSpace(stringValue(payload.Request["sessionKey"]))
	if sessionKey == "" {
		return
	}
	portal := m.resolvePortal(ctx, sessionKey)
	if portal == nil || portal.MXID == "" {
		return
	}
	body := "Tool approval required"
	command := strings.TrimSpace(stringValue(payload.Request["command"]))
	if command != "" {
		body = "Tool approval required: " + command
	}
	m.approvals.Register(payload.ID, time.Until(time.UnixMilli(payload.ExpiresAtMs)), &openClawPendingApprovalData{
		SessionKey:  sessionKey,
		Command:     command,
		CreatedAtMs: payload.CreatedAtMs,
		ExpiresAtMs: payload.ExpiresAtMs,
	})
	toolName := "exec"
	toolCallID := strings.TrimSpace(payload.ID)
	turnID := ""
	if pending := m.approvals.Get(strings.TrimSpace(payload.ID)); pending != nil {
		if data, _ := pending.Data.(*openClawPendingApprovalData); data != nil {
			if strings.TrimSpace(data.ToolCallID) != "" {
				toolCallID = strings.TrimSpace(data.ToolCallID)
			}
			if strings.TrimSpace(data.ToolName) != "" {
				toolName = strings.TrimSpace(data.ToolName)
			}
			turnID = strings.TrimSpace(data.TurnID)
		}
	}
	m.client.sendApprovalRequestFallbackEvent(
		ctx,
		portal,
		payload.ID,
		toolCallID,
		toolName,
		turnID,
		body,
		time.UnixMilli(payload.ExpiresAtMs),
	)
}

func (m *openClawManager) handleApprovalResolved(ctx context.Context, payload gatewayApprovalResolvedEvent) {
	approvalID := strings.TrimSpace(payload.ID)
	if approvalID == "" {
		return
	}
	pending := m.approvals.Get(approvalID)
	var data *openClawPendingApprovalData
	if pending != nil {
		data, _ = pending.Data.(*openClawPendingApprovalData)
	}
	sessionKey := strings.TrimSpace(stringValue(payload.Request["sessionKey"]))
	if sessionKey == "" && data != nil {
		sessionKey = strings.TrimSpace(data.SessionKey)
	}
	if sessionKey == "" {
		m.approvals.Drop(approvalID)
		return
	}
	portal := m.resolvePortal(ctx, sessionKey)
	if portal == nil || portal.MXID == "" {
		m.approvals.Drop(approvalID)
		return
	}
	if data != nil && strings.TrimSpace(data.TurnID) != "" && strings.TrimSpace(data.ToolCallID) != "" {
		approved, reason := openClawApprovalDecisionStatus(payload.Decision)
		m.client.EmitStreamPart(ctx, portal, data.TurnID, resolveOpenClawAgentID(portalMeta(portal), sessionKey, payload.Request), sessionKey, map[string]any{
			"type":       "tool-approval-response",
			"approvalId": approvalID,
			"toolCallId": data.ToolCallID,
			"approved":   approved,
			"reason":     reason,
		})
	} else {
		m.client.sendSystemNoticeViaPortal(ctx, portal, openClawApprovalResolvedText(payload.Decision))
	}
	if m.client != nil && m.client.approvalPrompts != nil {
		m.client.approvalPrompts.Drop(approvalID)
	}
	m.approvals.Drop(approvalID)
}

func (m *openClawManager) handleChatEvent(ctx context.Context, payload gatewayChatEvent) {
	if strings.TrimSpace(payload.SessionKey) == "" {
		return
	}
	portal := m.resolvePortal(ctx, payload.SessionKey)
	if portal == nil || portal.MXID == "" {
		return
	}
	meta := portalMeta(portal)
	payload.Message = normalizeOpenClawLiveMessage(payload.TS, payload.Message)
	eventTS := extractOpenClawEventTimestamp(payload.TS, payload.Message)
	if isOpenClawDirectChatEvent(payload.State, payload.Message) {
		m.handleDirectChatEvent(ctx, portal, meta, payload, eventTS)
		return
	}
	if openClawIsTerminalChatState(payload.State) {
		m.emitLatestUserMessageFromHistory(ctx, portal, meta, payload)
	}
	agentID := resolveOpenClawAgentID(meta, payload.SessionKey, payload.Message)
	maybePersistPortalAgentID(ctx, portal, meta, agentID)
	turnID := stringsTrimDefault(payload.RunID, "openclaw:"+payload.SessionKey)
	messageMetadata := openClawStreamMessageMetadata(meta, payload, agentID, turnID)
	if payload.State == "delta" {
		m.ensureStreamStart(ctx, portal, meta, turnID, payload.RunID, agentID, eventTS, messageMetadata)
		m.startRunRecovery(ctx, portal, meta, turnID, payload.RunID, agentID)
		text := extractMessageText(payload.Message)
		delta := m.client.computeVisibleDelta(turnID, text)
		if delta != "" {
			m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
				"timestamp": eventTS.UnixMilli(),
				"type":      "text-delta",
				"id":        "text-" + turnID,
				"delta":     delta,
			})
		}
		return
	}
	if openClawIsTerminalChatState(payload.State) {
		m.ensureStreamStart(ctx, portal, meta, turnID, payload.RunID, agentID, eventTS, messageMetadata)
		if usage := normalizeOpenClawUsage(payload.Usage); len(usage) > 0 {
			reasoningTokens := int64(0)
			if value, ok := openClawUsageInt64(usage, "prompt_tokens"); ok {
				meta.InputTokens = value
			}
			if value, ok := openClawUsageInt64(usage, "completion_tokens"); ok {
				meta.OutputTokens = value
			}
			if value, ok := openClawUsageInt64(usage, "reasoning_tokens"); ok {
				reasoningTokens = value
			}
			if value, ok := openClawUsageInt64(usage, "total_tokens"); ok {
				meta.TotalTokens = value
			} else {
				meta.TotalTokens = meta.InputTokens + meta.OutputTokens + reasoningTokens
			}
			meta.TotalTokensFresh = true
		}
		text := extractMessageText(payload.Message)
		if trimmed := strings.TrimSpace(text); trimmed != "" {
			meta.OpenClawPreviewSnippet = trimmed
			if !eventTS.IsZero() {
				meta.OpenClawLastPreviewAt = eventTS.UnixMilli()
			} else {
				meta.OpenClawLastPreviewAt = time.Now().UnixMilli()
			}
		}
		if delta := m.client.computeVisibleDelta(turnID, text); delta != "" {
			m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
				"timestamp": eventTS.UnixMilli(),
				"type":      "text-delta",
				"id":        "text-" + turnID,
				"delta":     delta,
			})
		}
		if payload.State == "error" {
			m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
				"timestamp": eventTS.UnixMilli(),
				"type":      "error",
				"errorText": openClawErrorText(payload),
			})
		} else if payload.State == "aborted" {
			m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
				"timestamp": eventTS.UnixMilli(),
				"type":      "abort",
				"reason":    stringsTrimDefault(payload.StopReason, "aborted"),
			})
		}
		m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
			"timestamp":       eventTS.UnixMilli(),
			"type":            "finish",
			"messageMetadata": messageMetadata,
		})
		m.client.FinishStream(turnID, payload.State)
		m.clearStartedTurn(turnID)
		m.untrackWaitingRun(payload.RunID)
		meta.LastLiveSeq = payload.Seq
		_ = portal.Save(ctx)
	}
}

func (m *openClawManager) handleDirectChatEvent(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, payload gatewayChatEvent, eventTS time.Time) {
	converted, sender, messageID := m.convertHistoryMessage(ctx, portal, meta, payload.Message)
	if converted == nil || messageID == "" {
		return
	}
	m.client.UserLogin.QueueRemoteEvent(&OpenClawRemoteMessage{
		portal:    portal.PortalKey,
		id:        messageID,
		sender:    sender,
		timestamp: eventTS,
		preBuilt:  converted,
	})
	if text := strings.TrimSpace(extractMessageText(payload.Message)); text != "" {
		meta.OpenClawPreviewSnippet = text
		if !eventTS.IsZero() {
			meta.OpenClawLastPreviewAt = eventTS.UnixMilli()
		} else {
			meta.OpenClawLastPreviewAt = time.Now().UnixMilli()
		}
		_ = portal.Save(ctx)
	}
}

func (m *openClawManager) emitLatestUserMessageFromHistory(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, payload gatewayChatEvent) {
	gateway := m.gatewayClient()
	if gateway == nil || portal == nil {
		return
	}
	history, err := gateway.RecentHistory(ctx, payload.SessionKey, 8)
	if err != nil || history == nil || len(history.Messages) == 0 {
		return
	}
	for idx := len(history.Messages) - 1; idx >= 0; idx-- {
		message := normalizeOpenClawLiveMessage(payload.TS, history.Messages[idx])
		if openClawMessageRole(message) != "user" {
			continue
		}
		converted, sender, messageID := m.convertHistoryMessage(ctx, portal, meta, message)
		if converted == nil || messageID == "" {
			return
		}
		eventTS := extractOpenClawEventTimestamp(payload.TS, message)
		m.client.UserLogin.QueueRemoteEvent(&OpenClawRemoteMessage{
			portal:    portal.PortalKey,
			id:        messageID,
			sender:    sender,
			timestamp: eventTS,
			preBuilt:  converted,
		})
		if text := strings.TrimSpace(extractMessageText(message)); text != "" {
			meta.OpenClawPreviewSnippet = text
			if !eventTS.IsZero() {
				meta.OpenClawLastPreviewAt = eventTS.UnixMilli()
			} else {
				meta.OpenClawLastPreviewAt = time.Now().UnixMilli()
			}
			_ = portal.Save(ctx)
		}
		return
	}
}

func (m *openClawManager) ensureStreamStart(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, turnID, runID, agentID string, eventTS time.Time, messageMetadata map[string]any) {
	if strings.TrimSpace(turnID) == "" {
		return
	}
	m.mu.Lock()
	if _, exists := m.started[turnID]; exists {
		m.mu.Unlock()
		return
	}
	m.started[turnID] = struct{}{}
	m.mu.Unlock()
	if agentID == "" {
		agentID = resolveOpenClawAgentID(meta, meta.OpenClawSessionKey, nil)
	}
	if len(messageMetadata) == 0 {
		messageMetadata = msgconv.BuildUIMessageMetadata(msgconv.UIMessageMetadataParams{
			TurnID:       turnID,
			AgentID:      agentID,
			CompletionID: runID,
		})
		if meta.OpenClawSessionID != "" {
			messageMetadata["session_id"] = meta.OpenClawSessionID
		}
		if meta.OpenClawSessionKey != "" {
			messageMetadata["session_key"] = meta.OpenClawSessionKey
		}
	}
	m.client.EmitStreamPart(ctx, portal, turnID, agentID, meta.OpenClawSessionKey, map[string]any{
		"timestamp":       eventTS.UnixMilli(),
		"type":            "start",
		"messageId":       turnID,
		"messageMetadata": messageMetadata,
	})
}

func (m *openClawManager) handleAgentEvent(ctx context.Context, payload gatewayAgentEvent) {
	if strings.TrimSpace(payload.SessionKey) == "" {
		return
	}
	portal := m.resolvePortal(ctx, payload.SessionKey)
	if portal == nil || portal.MXID == "" {
		return
	}
	meta := portalMeta(portal)
	agentID := resolveOpenClawAgentID(meta, payload.SessionKey, payload.Data)
	maybePersistPortalAgentID(ctx, portal, meta, agentID)
	turnID := stringsTrimDefault(payload.RunID, stringsTrimDefault(payload.SourceRunID, "openclaw:"+payload.SessionKey))
	agentMetadata := msgconv.BuildUIMessageMetadata(msgconv.UIMessageMetadataParams{
		TurnID:       turnID,
		AgentID:      agentID,
		CompletionID: payload.RunID,
	})
	if meta.OpenClawSessionID != "" {
		agentMetadata["session_id"] = meta.OpenClawSessionID
	}
	if payload.SessionKey != "" {
		agentMetadata["session_key"] = payload.SessionKey
	}
	eventTS := extractOpenClawEventTimestamp(payload.TS, nil)
	m.ensureStreamStart(ctx, portal, meta, turnID, payload.RunID, agentID, eventTS, agentMetadata)
	m.startRunRecovery(ctx, portal, meta, turnID, payload.RunID, agentID)
	stream := strings.ToLower(strings.TrimSpace(payload.Stream))
	switch stream {
	case "reasoning":
		if text := stringsTrimDefault(stringValue(payload.Data["text"]), stringValue(payload.Data["delta"])); text != "" {
			m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
				"timestamp": eventTS.UnixMilli(),
				"type":      "reasoning-delta",
				"id":        "reasoning-" + turnID,
				"delta":     text,
			})
		}
	case "tool":
		toolCallID := stringsTrimDefault(stringValue(payload.Data["toolCallId"]), stringsTrimDefault(stringValue(payload.Data["toolUseId"]), stringValue(payload.Data["id"])))
		toolName := stringsTrimDefault(stringValue(payload.Data["toolName"]), stringsTrimDefault(stringValue(payload.Data["name"]), "tool"))
		if toolCallID != "" {
			if input, ok := payload.Data["input"]; ok {
				m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
					"timestamp":        eventTS.UnixMilli(),
					"type":             "tool-input-available",
					"toolCallId":       toolCallID,
					"toolName":         toolName,
					"input":            input,
					"providerExecuted": true,
				})
			}
			if approvalID := strings.TrimSpace(stringValue(payload.Data["approvalId"])); approvalID != "" {
				m.attachApprovalContext(approvalID, payload.SessionKey, turnID, toolCallID, toolName)
				m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
					"timestamp":  eventTS.UnixMilli(),
					"type":       "tool-approval-request",
					"approvalId": approvalID,
					"toolCallId": toolCallID,
				})
			}
			if output, ok := payload.Data["output"]; ok {
				m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
					"timestamp":        eventTS.UnixMilli(),
					"type":             "tool-output-available",
					"toolCallId":       toolCallID,
					"output":           output,
					"providerExecuted": true,
				})
				m.ensureSpawnedSessionPortal(ctx, openClawSpawnedSessionKeyFromToolResult(toolName, output))
			} else if result, ok := payload.Data["result"]; ok {
				m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
					"timestamp":        eventTS.UnixMilli(),
					"type":             "tool-output-available",
					"toolCallId":       toolCallID,
					"output":           result,
					"providerExecuted": true,
				})
				m.ensureSpawnedSessionPortal(ctx, openClawSpawnedSessionKeyFromToolResult(toolName, result))
			}
			if errText := strings.TrimSpace(stringValue(payload.Data["error"])); errText != "" {
				m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
					"timestamp":        eventTS.UnixMilli(),
					"type":             "tool-output-error",
					"toolCallId":       toolCallID,
					"errorText":        errText,
					"providerExecuted": true,
				})
			}
			return
		}
		fallthrough
	default:
		m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
			"timestamp": eventTS.UnixMilli(),
			"type":      "data-openclaw-" + stream,
			"id":        fmt.Sprintf("openclaw-%s-%d", stream, payload.Seq),
			"data":      map[string]any{"stream": payload.Stream, "data": payload.Data},
		})
	}
}

func (m *openClawManager) ensureSpawnedSessionPortal(ctx context.Context, sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}

	// Queue a portal resync immediately so persistent child sessions materialize
	// as their own rooms instead of waiting for later child traffic.
	m.resolvePortal(m.client.BackgroundContext(ctx), sessionKey)

	go func() {
		if err := m.syncSessions(m.client.BackgroundContext(ctx)); err != nil {
			m.client.Log().Debug().Err(err).Str("session_key", sessionKey).Msg("Failed to refresh OpenClaw sessions after spawned session detection")
		}
	}()
}

func openClawSpawnedSessionKeyFromToolResult(toolName string, value any) string {
	if !strings.EqualFold(strings.TrimSpace(toolName), "sessions_spawn") {
		return ""
	}
	return openClawExtractSpawnedSessionKey(value)
}

func openClawExtractSpawnedSessionKey(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if childSessionKey := strings.TrimSpace(stringValue(typed["childSessionKey"])); isOpenClawSpawnedSessionKey(childSessionKey) {
			return childSessionKey
		}
		for _, nestedKey := range []string{"result", "output", "payload", "data"} {
			if nested := openClawExtractSpawnedSessionKey(typed[nestedKey]); nested != "" {
				return nested
			}
		}
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return ""
		}
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			var parsed map[string]any
			if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
				return openClawExtractSpawnedSessionKey(parsed)
			}
		}
		if isOpenClawSpawnedSessionKey(trimmed) {
			return trimmed
		}
	}
	return ""
}

func isOpenClawSpawnedSessionKey(sessionKey string) bool {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return false
	}
	return strings.Contains(sessionKey, ":subagent:") || strings.Contains(sessionKey, ":acp:")
}

func (m *openClawManager) attachApprovalContext(approvalID, sessionKey, turnID, toolCallID, toolName string) {
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return
	}
	m.approvals.SetData(approvalID, func(data any) any {
		pending, _ := data.(*openClawPendingApprovalData)
		if pending == nil {
			pending = &openClawPendingApprovalData{}
		}
		if strings.TrimSpace(sessionKey) != "" {
			pending.SessionKey = strings.TrimSpace(sessionKey)
		}
		if strings.TrimSpace(turnID) != "" {
			pending.TurnID = strings.TrimSpace(turnID)
		}
		if strings.TrimSpace(toolCallID) != "" {
			pending.ToolCallID = strings.TrimSpace(toolCallID)
		}
		if strings.TrimSpace(toolName) != "" {
			pending.ToolName = strings.TrimSpace(toolName)
		}
		return pending
	})
}

func (m *openClawManager) startRunRecovery(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, turnID, runID, agentID string) {
	runID = strings.TrimSpace(runID)
	if runID == "" || portal == nil || portal.MXID == "" {
		return
	}
	if !m.trackWaitingRun(runID) {
		return
	}
	go m.waitForRunCompletion(m.client.BackgroundContext(ctx), portal, meta, turnID, runID, agentID)
}

func (m *openClawManager) waitForRunCompletion(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, turnID, runID, agentID string) {
	defer m.untrackWaitingRun(runID)

	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}

	if !m.client.isStreamActive(turnID) {
		return
	}
	gateway := m.gatewayClient()
	if gateway == nil {
		return
	}
	waitResp, err := gateway.WaitForRun(ctx, runID, 30*time.Second)
	if err != nil || waitResp == nil || !m.client.isStreamActive(turnID) {
		return
	}
	status := strings.ToLower(strings.TrimSpace(waitResp.Status))
	if status == "" || status == "timeout" {
		return
	}

	recoveredText := m.recoverRunText(ctx, meta.OpenClawSessionKey, turnID)
	if recoveredText == "" {
		recoveredText = m.recoverRunPreview(ctx, portal, meta)
	}
	if recoveredText != "" {
		if delta := m.client.computeVisibleDelta(turnID, recoveredText); delta != "" {
			m.client.EmitStreamPart(ctx, portal, turnID, agentID, meta.OpenClawSessionKey, map[string]any{
				"type":  "text-delta",
				"id":    "text-" + turnID,
				"delta": delta,
			})
		}
	}

	metadata := msgconv.BuildUIMessageMetadata(msgconv.UIMessageMetadataParams{
		TurnID:        turnID,
		AgentID:       agentID,
		CompletionID:  runID,
		FinishReason:  status,
		StartedAtMs:   waitResp.StartedAt,
		CompletedAtMs: waitResp.EndedAt,
		IncludeUsage:  true,
	})
	if meta.OpenClawSessionID != "" {
		metadata["session_id"] = meta.OpenClawSessionID
	}
	if meta.OpenClawSessionKey != "" {
		metadata["session_key"] = meta.OpenClawSessionKey
	}
	if strings.TrimSpace(waitResp.Error) != "" {
		metadata["error_text"] = strings.TrimSpace(waitResp.Error)
	}
	switch status {
	case "error":
		m.client.EmitStreamPart(ctx, portal, turnID, agentID, meta.OpenClawSessionKey, map[string]any{
			"type":      "error",
			"errorText": stringsTrimDefault(waitResp.Error, "OpenClaw run failed"),
		})
	default:
		m.client.EmitStreamPart(ctx, portal, turnID, agentID, meta.OpenClawSessionKey, map[string]any{
			"type":            "finish",
			"messageMetadata": metadata,
		})
		m.client.FinishStream(turnID, status)
		m.clearStartedTurn(turnID)
		return
	}
	m.client.EmitStreamPart(ctx, portal, turnID, agentID, meta.OpenClawSessionKey, map[string]any{
		"type":            "finish",
		"messageMetadata": metadata,
	})
	m.client.FinishStream(turnID, status)
	m.clearStartedTurn(turnID)
}

func (m *openClawManager) recoverRunText(ctx context.Context, sessionKey, turnID string) string {
	gateway := m.gatewayClient()
	if gateway == nil || strings.TrimSpace(sessionKey) == "" {
		return ""
	}
	history, err := gateway.RecentHistory(ctx, sessionKey, 25)
	if err != nil || history == nil {
		return ""
	}
	filtered := history.Messages
	if trimmedTurnID := strings.TrimSpace(turnID); trimmedTurnID != "" {
		filtered = make([]map[string]any, 0, len(history.Messages))
		for _, message := range history.Messages {
			if strings.EqualFold(historyMessageTurnID(message), trimmedTurnID) {
				filtered = append(filtered, message)
			}
		}
		if len(filtered) == 0 {
			filtered = history.Messages
		}
	}
	for i := len(filtered) - 1; i >= 0; i-- {
		message := filtered[i]
		role := strings.ToLower(strings.TrimSpace(stringValue(message["role"])))
		if role != "assistant" && role != "toolresult" {
			continue
		}
		text := extractMessageText(message)
		if strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func (m *openClawManager) recoverRunPreview(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) string {
	if m == nil || m.client == nil || meta == nil {
		return ""
	}
	snippet := strings.TrimSpace(m.client.previewSessionSnippet(ctx, meta.OpenClawSessionKey))
	if snippet == "" {
		return ""
	}
	meta.OpenClawPreviewSnippet = snippet
	meta.OpenClawLastPreviewAt = time.Now().UnixMilli()
	if portal != nil {
		_ = portal.Save(ctx)
	}
	return snippet
}

func (m *openClawManager) resolvePortal(ctx context.Context, sessionKey string) *bridgev2.Portal {
	if strings.TrimSpace(sessionKey) == "" {
		return nil
	}
	key := m.client.portalKeyForSession(sessionKey)
	portal, err := m.client.UserLogin.Bridge.GetPortalByKey(ctx, key)
	if err == nil && portal != nil {
		m.clearPendingPortalResync(sessionKey)
		return portal
	}
	m.mu.RLock()
	session, ok := m.sessions[sessionKey]
	m.mu.RUnlock()
	if !ok {
		session = gatewaySessionRow{Key: sessionKey, SessionID: sessionKey}
	}
	if m.shouldQueuePortalResync(sessionKey) {
		m.client.UserLogin.QueueRemoteEvent(&OpenClawSessionResyncEvent{client: m.client, session: session})
	}
	portal, _ = m.client.UserLogin.Bridge.GetPortalByKey(ctx, key)
	if portal != nil {
		m.clearPendingPortalResync(sessionKey)
	}
	return portal
}

var openClawMissingMessageTimestamp = time.Unix(0, 0).UTC()

func openClawSessionTimestamp(session gatewaySessionRow) time.Time {
	if session.UpdatedAt > 0 {
		return time.UnixMilli(session.UpdatedAt)
	}
	return time.Time{}
}

func extractMessageTimestamp(message map[string]any) time.Time {
	if ts, ok := message["timestamp"].(float64); ok && ts > 0 {
		return time.UnixMilli(int64(ts))
	}
	if ts, ok := message["timestamp"].(int64); ok && ts > 0 {
		return time.UnixMilli(ts)
	}
	if ts, ok := message["timestamp"].(int); ok && ts > 0 {
		return time.UnixMilli(int64(ts))
	}
	if ts, ok := message["timestamp"].(string); ok {
		ts = strings.TrimSpace(ts)
		if ts != "" {
			if unixMilli, err := strconv.ParseInt(ts, 10, 64); err == nil && unixMilli > 0 {
				return time.UnixMilli(unixMilli)
			}
			if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
				return parsed
			}
			if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
				return parsed
			}
		}
	}
	return openClawMissingMessageTimestamp
}

func openClawMessageStringField(message map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(stringValue(message[key])); value != "" {
			return value
		}
	}
	nested := jsonutil.ToMap(message["message"])
	for _, key := range keys {
		if value := strings.TrimSpace(stringValue(nested[key])); value != "" {
			return value
		}
	}
	return ""
}

func openClawMessageRole(message map[string]any) string {
	role := strings.ToLower(strings.TrimSpace(openClawMessageStringField(message, "role")))
	if role == "human" {
		return "user"
	}
	return role
}

func openClawIsTerminalChatState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "final", "done", "complete", "completed", "aborted", "error":
		return true
	default:
		return false
	}
}

func historyMessageTurnID(message map[string]any) string {
	return strings.TrimSpace(stringsTrimDefault(
		openClawMessageStringField(message, "turnId", "turn_id"),
		stringsTrimDefault(
			openClawMessageStringField(message, "runId", "run_id"),
			openClawMessageStringField(message, "id"),
		),
	))
}

func (m *openClawManager) clearStartedTurn(turnID string) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return
	}
	m.mu.Lock()
	delete(m.started, turnID)
	m.mu.Unlock()
}

func (m *openClawManager) shouldQueuePortalResync(sessionKey string) bool {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return false
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if last, ok := m.resyncing[sessionKey]; ok && now.Sub(last) < 5*time.Second {
		return false
	}
	m.resyncing[sessionKey] = now
	return true
}

func (m *openClawManager) clearPendingPortalResync(sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	m.mu.Lock()
	delete(m.resyncing, sessionKey)
	m.mu.Unlock()
}

func extractMessageText(message map[string]any) string {
	return openclawconv.ExtractMessageText(message)
}

func contentBlocks(message map[string]any) []map[string]any {
	return openclawconv.ContentBlocks(message)
}

func stringValue(v any) string {
	switch typed := v.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}

func openClawAttachmentFallbackText(block map[string]any, err error) string {
	name := openClawBlockFilename(block)
	if name == "" {
		name = "attachment"
	}
	if err == nil {
		return "[Attachment: " + name + "]"
	}
	return fmt.Sprintf("[Attachment unavailable: %s (%v)]", name, err)
}

func convertHistoryToCanonicalUI(message map[string]any, role string, meta *PortalMetadata) ([]map[string]any, map[string]any) {
	agentID := resolveOpenClawAgentID(meta, stringsTrimDefault(meta.OpenClawSessionKey, stringValue(message["sessionKey"])), message)
	turnID := strings.TrimSpace(stringsTrimDefault(
		stringValue(message["turnId"]),
		stringsTrimDefault(stringValue(message["runId"]), stringValue(message["id"])),
	))
	params := msgconv.UIMessageMetadataParams{
		TurnID:       turnID,
		AgentID:      agentID,
		Model:        stringsTrimDefault(stringValue(message["model"]), meta.Model),
		FinishReason: stringsTrimDefault(stringValue(message["finishReason"]), stringValue(message["stopReason"])),
		CompletionID: stringValue(message["runId"]),
		IncludeUsage: true,
	}
	if usage := normalizeOpenClawUsage(jsonutil.ToMap(message["usage"])); len(usage) > 0 {
		if value, ok := openClawUsageInt64(usage, "prompt_tokens"); ok {
			params.PromptTokens = value
		}
		if value, ok := openClawUsageInt64(usage, "completion_tokens"); ok {
			params.CompletionTokens = value
		}
		if value, ok := openClawUsageInt64(usage, "reasoning_tokens"); ok {
			params.ReasoningTokens = value
		}
		if value, ok := openClawUsageInt64(usage, "total_tokens"); ok {
			params.TotalTokens = value
		}
	}
	metadata := msgconv.BuildUIMessageMetadata(params)
	if sessionID := stringsTrimDefault(stringValue(message["sessionId"]), meta.OpenClawSessionID); sessionID != "" {
		metadata["session_id"] = sessionID
	}
	if sessionKey := stringsTrimDefault(stringValue(message["sessionKey"]), meta.OpenClawSessionKey); sessionKey != "" {
		metadata["session_key"] = sessionKey
	}
	if errorText := stringsTrimDefault(stringValue(message["errorMessage"]), stringValue(message["error"])); errorText != "" {
		metadata["error_text"] = errorText
	}
	return openClawHistoryUIParts(message, role), metadata
}

func openClawHistoryUIParts(message map[string]any, role string) []map[string]any {
	state := &streamui.UIState{
		TurnID: stringsTrimDefault(
			stringValue(message["turnId"]),
			stringsTrimDefault(stringValue(message["runId"]), "history"),
		),
	}
	openClawApplyHistoryChunks(state, message, role)
	snapshot := streamui.SnapshotCanonicalUIMessage(state)
	return normalizeOpenClawUIParts(snapshot["parts"])
}

func openClawApplyHistoryChunks(state *streamui.UIState, message map[string]any, role string) {
	if state == nil {
		return
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "toolresult" {
		openClawApplyHistoryToolResult(state, message)
		return
	}
	blocks := contentBlocks(message)
	for idx, block := range blocks {
		blockType := strings.ToLower(strings.TrimSpace(stringValue(block["type"])))
		switch blockType {
		case "text", "input_text", "output_text":
			text := strings.TrimSpace(stringsTrimDefault(stringValue(block["text"]), stringValue(block["content"])))
			if text == "" {
				continue
			}
			partID := fmt.Sprintf("text-%d", idx)
			streamui.ApplyChunk(state, map[string]any{"type": "text-start", "id": partID})
			streamui.ApplyChunk(state, map[string]any{"type": "text-delta", "id": partID, "delta": text})
			streamui.ApplyChunk(state, map[string]any{"type": "text-end", "id": partID})
		case "reasoning", "thinking":
			text := strings.TrimSpace(stringsTrimDefault(stringValue(block["text"]), stringValue(block["content"])))
			if text == "" {
				continue
			}
			partID := fmt.Sprintf("reasoning-%d", idx)
			streamui.ApplyChunk(state, map[string]any{"type": "reasoning-start", "id": partID})
			streamui.ApplyChunk(state, map[string]any{"type": "reasoning-delta", "id": partID, "delta": text})
			streamui.ApplyChunk(state, map[string]any{"type": "reasoning-end", "id": partID})
		case "toolcall", "tooluse", "functioncall":
			toolCallID := strings.TrimSpace(stringsTrimDefault(stringValue(block["id"]), stringValue(block["call_id"])))
			if toolCallID == "" {
				toolCallID = fmt.Sprintf("tool-call-%d", idx)
			}
			toolName := strings.TrimSpace(stringsTrimDefault(stringValue(block["name"]), stringValue(block["toolName"])))
			input := jsonutil.ToMap(block["arguments"])
			if len(input) == 0 {
				input = jsonutil.ToMap(block["input"])
			}
			streamui.ApplyChunk(state, map[string]any{
				"type":       "tool-input-available",
				"toolCallId": toolCallID,
				"toolName":   stringsTrimDefault(toolName, "tool"),
				"input":      input,
			})
			if approvalID := strings.TrimSpace(stringsTrimDefault(stringValue(block["approvalId"]), stringValue(jsonutil.ToMap(block["approval"])["id"]))); approvalID != "" {
				streamui.ApplyChunk(state, map[string]any{
					"type":       "tool-approval-request",
					"approvalId": approvalID,
					"toolCallId": toolCallID,
				})
			}
		case "toolresult", "tool_result", "tool-output":
			openClawApplyHistoryToolResult(state, block)
		}
	}
	if len(blocks) == 0 {
		if text := strings.TrimSpace(extractMessageText(message)); text != "" {
			streamui.ApplyChunk(state, map[string]any{"type": "text-start", "id": "text-history"})
			streamui.ApplyChunk(state, map[string]any{"type": "text-delta", "id": "text-history", "delta": text})
			streamui.ApplyChunk(state, map[string]any{"type": "text-end", "id": "text-history"})
		}
	}
}

func openClawApplyHistoryToolResult(state *streamui.UIState, message map[string]any) {
	toolCallID := strings.TrimSpace(stringsTrimDefault(stringValue(message["toolCallId"]), stringValue(message["toolUseId"])))
	if toolCallID == "" {
		toolCallID = "tool-result"
	}
	toolName := strings.TrimSpace(stringsTrimDefault(stringValue(message["toolName"]), stringValue(message["name"])))
	if toolName != "" {
		streamui.ApplyChunk(state, map[string]any{
			"type":       "tool-input-available",
			"toolCallId": toolCallID,
			"toolName":   toolName,
			"input":      jsonutil.DeepCloneAny(jsonutil.ToMap(message["input"])),
		})
	}
	if approvalID := strings.TrimSpace(stringsTrimDefault(stringValue(message["approvalId"]), stringValue(jsonutil.ToMap(message["approval"])["id"]))); approvalID != "" {
		streamui.ApplyChunk(state, map[string]any{
			"type":       "tool-approval-request",
			"approvalId": approvalID,
			"toolCallId": toolCallID,
		})
	}
	if isError, _ := message["isError"].(bool); isError {
		streamui.ApplyChunk(state, map[string]any{
			"type":       "tool-output-error",
			"toolCallId": toolCallID,
			"errorText":  stringsTrimDefault(extractMessageText(message), stringValue(message["error"])),
		})
		return
	}
	output := jsonutil.DeepCloneAny(message["details"])
	if output == nil {
		output = jsonutil.DeepCloneAny(stringsTrimDefault(extractMessageText(message), stringValue(message["result"])))
	}
	streamui.ApplyChunk(state, map[string]any{
		"type":       "tool-output-available",
		"toolCallId": toolCallID,
		"output":     output,
	})
}

func normalizeOpenClawUIParts(raw any) []map[string]any {
	switch typed := raw.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			part := jsonutil.ToMap(item)
			if len(part) == 0 {
				continue
			}
			out = append(out, part)
		}
		return out
	default:
		return nil
	}
}

func openClawHistoryFallbackText(uiParts []map[string]any) string {
	for _, part := range uiParts {
		partType := strings.TrimSpace(stringValue(part["type"]))
		switch partType {
		case "text", "reasoning":
			if text := strings.TrimSpace(stringValue(part["text"])); text != "" {
				return text
			}
		case "dynamic-tool":
			toolName := strings.TrimSpace(stringsTrimDefault(stringValue(part["toolName"]), "tool"))
			switch strings.TrimSpace(stringValue(part["state"])) {
			case "approval-requested":
				return "Tool approval required: " + toolName
			case "output-error":
				return "Tool failed: " + toolName
			case "output-available":
				return "Tool completed: " + toolName
			default:
				return "Tool activity: " + toolName
			}
		}
	}
	return ""
}

func isOpenClawAttachmentBlock(block map[string]any) bool {
	return openclawconv.IsAttachmentBlock(block)
}

func resolveOpenClawAgentID(meta *PortalMetadata, sessionKey string, payload map[string]any) string {
	for _, key := range []string{"agentId", "agent_id", "agent"} {
		if payload != nil {
			if value := strings.TrimSpace(stringValue(payload[key])); value != "" {
				return value
			}
		}
	}
	if meta != nil && strings.TrimSpace(meta.OpenClawAgentID) != "" {
		return strings.TrimSpace(meta.OpenClawAgentID)
	}
	if value := openClawAgentIDFromSessionKey(sessionKey); value != "" {
		return value
	}
	return "gateway"
}

func maybePersistPortalAgentID(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, agentID string) {
	agentID = strings.TrimSpace(agentID)
	if portal == nil || meta == nil || agentID == "" || meta.OpenClawAgentID == agentID {
		return
	}
	meta.OpenClawAgentID = agentID
	_ = portal.Save(ctx)
}
