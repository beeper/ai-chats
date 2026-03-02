package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/bridges/codex/codexrpc"
	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
	"github.com/beeper/ai-bridge/pkg/connector/msgconv"
	"github.com/beeper/ai-bridge/pkg/matrixevents"
	"github.com/beeper/ai-bridge/pkg/shared/citations"
	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
	"github.com/beeper/ai-bridge/pkg/shared/streamui"
	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

var _ bridgev2.NetworkAPI = (*CodexClient)(nil)
var _ bridgev2.DeleteChatHandlingNetworkAPI = (*CodexClient)(nil)
var _ bridgev2.ActionResponseHandlingNetworkAPI = (*CodexClient)(nil)

const codexGhostID = networkid.UserID("codex")

type codexNotif struct {
	Method string
	Params json.RawMessage
}

func codexTurnKey(threadID, turnID string) string {
	return strings.TrimSpace(threadID) + "\n" + strings.TrimSpace(turnID)
}

type codexActiveTurn struct {
	portal   *bridgev2.Portal
	meta     *PortalMetadata
	state    *streamingState
	threadID string
	turnID   string
	model    string
}

type codexPendingMessage struct {
	event  *event.Event
	portal *bridgev2.Portal
	meta   *PortalMetadata
	body   string
}

type CodexClient struct {
	UserLogin *bridgev2.UserLogin
	connector *CodexConnector
	log       zerolog.Logger

	rpcMu sync.Mutex
	rpc   *codexrpc.Client

	notifCh   chan codexNotif
	notifDone chan struct{} // closed on Disconnect to stop dispatchNotifications

	loggedIn atomic.Bool

	// streamEventHook, when set, receives the stream event envelope (including "part")
	// instead of sending ephemeral Matrix events. Used by tests.
	streamEventHook func(turnID string, seq int, content map[string]any, txnID string)

	activeMu    sync.Mutex
	activeTurns map[string]*codexActiveTurn // turnKey(threadId, turnId) -> active turn (for approvals)

	subMu        sync.Mutex
	turnSubs     map[string]chan codexNotif // turnKey(threadId, turnId) -> notification channel
	dispatchOnce sync.Once

	loadedMu      sync.Mutex
	loadedThreads map[string]bool // threadId -> loaded via thread/start|thread/resume

	approvals *bridgeadapter.ApprovalManager[ToolApprovalDecisionCodex]

	roomMu          sync.Mutex
	activeRooms     map[id.RoomID]bool
	pendingMessages map[id.RoomID]*codexPendingMessage

	streamFallbackToDebounced atomic.Bool
}

func newCodexClient(login *bridgev2.UserLogin, connector *CodexConnector) (*CodexClient, error) {
	if login == nil {
		return nil, errors.New("missing login")
	}
	if connector == nil {
		return nil, errors.New("missing connector for CodexClient")
	}
	meta := loginMetadata(login)
	if !strings.EqualFold(strings.TrimSpace(meta.Provider), ProviderCodex) {
		return nil, fmt.Errorf("invalid provider for CodexClient: %s", meta.Provider)
	}
	if strings.TrimSpace(meta.CodexHome) == "" {
		return nil, errors.New("missing codex_home in login metadata")
	}
	log := login.Log.With().Str("component", "codex").Logger()
	return &CodexClient{
		UserLogin:       login,
		connector:       connector,
		log:             log,
		notifCh:         make(chan codexNotif, 4096),
		notifDone:       make(chan struct{}),
		approvals:       bridgeadapter.NewApprovalManager[ToolApprovalDecisionCodex](),
		loadedThreads:   make(map[string]bool),
		activeTurns:     make(map[string]*codexActiveTurn),
		turnSubs:        make(map[string]chan codexNotif),
		activeRooms:     make(map[id.RoomID]bool),
		pendingMessages: make(map[id.RoomID]*codexPendingMessage),
	}, nil
}

func (cc *CodexClient) loggerForContext(ctx context.Context) *zerolog.Logger {
	return bridgeadapter.LoggerFromContext(ctx, &cc.log)
}

func (cc *CodexClient) Connect(ctx context.Context) {
	cc.loggedIn.Store(false)
	if err := cc.ensureRPC(cc.backgroundContext(ctx)); err != nil {
		cc.UserLogin.BridgeState.Send(status.BridgeState{
			StateEvent: status.StateTransientDisconnect,
			Error:      AIAuthFailed,
			Message:    fmt.Sprintf("Codex isn't available: %v", err),
		})
		return
	}

	// Best-effort account/read.
	readCtx, cancel := context.WithTimeout(cc.backgroundContext(ctx), 10*time.Second)
	defer cancel()
	var resp struct {
		Account *struct {
			Type  string `json:"type"`
			Email string `json:"email"`
		} `json:"account"`
		RequiresOpenaiAuth bool `json:"requiresOpenaiAuth"`
	}
	_ = cc.rpc.Call(readCtx, "account/read", map[string]any{"refreshToken": false}, &resp)
	if resp.Account != nil {
		cc.loggedIn.Store(true)
		meta := loginMetadata(cc.UserLogin)
		if strings.TrimSpace(resp.Account.Email) != "" {
			meta.CodexAccountEmail = strings.TrimSpace(resp.Account.Email)
			_ = cc.UserLogin.Save(cc.backgroundContext(ctx))
		}
	}

	cc.UserLogin.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateConnected,
		Message:    "Connected",
	})

	// Ensure default Codex chat + thread.
	if err := cc.ensureDefaultCodexChat(cc.backgroundContext(ctx)); err != nil {
		cc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to ensure default Codex chat")
	}
}

func (cc *CodexClient) Disconnect() {
	cc.loggedIn.Store(false)

	// Signal dispatchNotifications goroutine to stop.
	if cc.notifDone != nil {
		select {
		case <-cc.notifDone:
			// Already closed.
		default:
			close(cc.notifDone)
		}
	}

	cc.rpcMu.Lock()
	if cc.rpc != nil {
		_ = cc.rpc.Close()
		cc.rpc = nil
	}
	cc.rpcMu.Unlock()

	cc.loadedMu.Lock()
	cc.loadedThreads = make(map[string]bool)
	cc.loadedMu.Unlock()

	cc.activeMu.Lock()
	cc.activeTurns = make(map[string]*codexActiveTurn)
	cc.activeMu.Unlock()

	cc.subMu.Lock()
	cc.turnSubs = make(map[string]chan codexNotif)
	cc.subMu.Unlock()

	cc.roomMu.Lock()
	cc.activeRooms = make(map[id.RoomID]bool)
	cc.pendingMessages = make(map[id.RoomID]*codexPendingMessage)
	cc.roomMu.Unlock()
}

func (cc *CodexClient) IsLoggedIn() bool {
	return cc.loggedIn.Load()
}

func (cc *CodexClient) LogoutRemote(ctx context.Context) {
	// Best-effort: ask Codex to forget the account (tokens are managed by Codex under CODEX_HOME).
	if err := cc.ensureRPC(cc.backgroundContext(ctx)); err == nil && cc.rpc != nil {
		callCtx, cancel := context.WithTimeout(cc.backgroundContext(ctx), 10*time.Second)
		defer cancel()
		var out map[string]any
		_ = cc.rpc.Call(callCtx, "account/logout", nil, &out)
	}
	// Best-effort: remove on-disk Codex state for this login.
	cc.purgeCodexHomeBestEffort(ctx)
	// Best-effort: remove on-disk per-room Codex working dirs.
	cc.purgeCodexCwdsBestEffort(ctx)

	cc.Disconnect()

	if cc.connector != nil {
		bridgeadapter.RemoveClientFromCache(&cc.connector.clientsMu, cc.connector.clients, cc.UserLogin.ID)
	}

	cc.UserLogin.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateLoggedOut,
		Message:    "Disconnected by user",
	})
}

func (cc *CodexClient) purgeCodexHomeBestEffort(ctx context.Context) {
	if cc.UserLogin == nil {
		return
	}
	meta, ok := cc.UserLogin.Metadata.(*UserLoginMetadata)
	if !ok || meta == nil {
		return
	}
	// Don't delete unmanaged homes (e.g. the user's own ~/.codex).
	if !meta.CodexHomeManaged {
		return
	}
	codexHome := strings.TrimSpace(meta.CodexHome)
	if codexHome == "" {
		return
	}
	// Safety: refuse to delete suspicious paths.
	clean := filepath.Clean(codexHome)
	if clean == string(os.PathSeparator) || clean == "." {
		return
	}
	// Best-effort recursive delete.
	_ = os.RemoveAll(clean)
}

func (cc *CodexClient) purgeCodexCwdsBestEffort(ctx context.Context) {
	if cc.UserLogin == nil || cc.UserLogin.Bridge == nil || cc.UserLogin.Bridge.DB == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// Enumerate portal metadata before bridgev2 deletes the portal rows.
	ups, err := cc.UserLogin.Bridge.DB.UserPortal.GetAllForLogin(ctx, cc.UserLogin.UserLogin)
	if err != nil || len(ups) == 0 {
		return
	}

	tmp := filepath.Clean(os.TempDir())
	if tmp == "" || tmp == "." || tmp == string(os.PathSeparator) {
		// Should never happen, but avoid deleting arbitrary dirs if it does.
		return
	}

	seen := make(map[string]struct{})
	for _, up := range ups {
		if up == nil {
			continue
		}
		portal, err := cc.UserLogin.Bridge.GetExistingPortalByKey(ctx, up.Portal)
		if err != nil || portal == nil || portal.Metadata == nil {
			continue
		}
		meta, ok := portal.Metadata.(*PortalMetadata)
		if !ok || meta == nil {
			continue
		}
		cwd := strings.TrimSpace(meta.CodexCwd)
		if cwd == "" {
			continue
		}
		clean := filepath.Clean(cwd)
		if clean == "." || clean == string(os.PathSeparator) {
			continue
		}
		// Safety: only delete dirs we created via os.MkdirTemp("", "ai-bridge-codex-*").
		if !strings.HasPrefix(filepath.Base(clean), "ai-bridge-codex-") {
			continue
		}
		if !strings.HasPrefix(clean, tmp+string(os.PathSeparator)) {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		_ = os.RemoveAll(clean)
	}
}

func (cc *CodexClient) IsThisUser(ctx context.Context, userID networkid.UserID) bool {
	return userID == humanUserID(cc.UserLogin.ID)
}

func (cc *CodexClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	meta := portalMeta(portal)
	title := meta.Title
	if title == "" {
		if portal.Name != "" {
			title = portal.Name
		} else {
			title = "Codex"
		}
	}
	return &bridgev2.ChatInfo{
		Name:  ptr.Ptr(title),
		Topic: ptr.NonZero(portal.Topic),
	}, nil
}

func (cc *CodexClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	if ghost == nil {
		return &bridgev2.UserInfo{Name: ptr.Ptr("Codex")}, nil
	}
	if ghost.ID == codexGhostID {
		return &bridgev2.UserInfo{
			Name:        ptr.Ptr("Codex"),
			IsBot:       ptr.Ptr(true),
			Identifiers: []string{"codex"},
		}, nil
	}
	return &bridgev2.UserInfo{Name: ptr.Ptr("Codex")}, nil
}

func (cc *CodexClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	return aiBaseCaps
}

func (cc *CodexClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	if msg == nil || msg.Content == nil || msg.Portal == nil || msg.Event == nil {
		return nil, errors.New("invalid message")
	}
	portal := msg.Portal
	meta := portalMeta(portal)
	if meta == nil || !meta.IsCodexRoom {
		return nil, bridgeadapter.UnsupportedMessageStatus(errors.New("not a Codex room"))
	}
	if bridgeadapter.IsMatrixBotUser(ctx, cc.UserLogin.Bridge, msg.Event.Sender) {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	// Only text messages.
	switch msg.Content.MsgType {
	case event.MsgText, event.MsgNotice, event.MsgEmote:
	default:
		return nil, bridgeadapter.UnsupportedMessageStatus(fmt.Errorf("%s messages are not supported", msg.Content.MsgType))
	}
	if msg.Content.RelatesTo != nil && msg.Content.RelatesTo.GetReplaceID() != "" {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	body := strings.TrimSpace(msg.Content.Body)
	if body == "" {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	if err := cc.ensureRPC(cc.backgroundContext(ctx)); err != nil {
		return nil, messageSendStatusError(err, "Codex isn't available. Sign in again.", "")
	}
	if strings.TrimSpace(meta.CodexThreadID) == "" || strings.TrimSpace(meta.CodexCwd) == "" {
		if err := cc.ensureCodexThread(ctx, portal, meta); err != nil {
			return nil, messageSendStatusError(err, "Codex thread unavailable. Try !ai reset.", "")
		}
	}
	if err := cc.ensureCodexThreadLoaded(ctx, portal, meta); err != nil {
		return nil, messageSendStatusError(err, "Codex thread unavailable. Try !ai reset.", "")
	}

	roomID := portal.MXID
	if roomID == "" {
		return nil, errors.New("portal has no room id")
	}

	// Save user message immediately; we return Pending=true.
	userMsg := &database.Message{
		ID:        networkid.MessageID(fmt.Sprintf("mx:%s", string(msg.Event.ID))),
		MXID:      msg.Event.ID,
		Room:      portal.PortalKey,
		SenderID:  humanUserID(cc.UserLogin.ID),
		Timestamp: bridgeadapter.MatrixEventTimestamp(msg.Event),
		Metadata: &MessageMetadata{
			Role: "user",
			Body: body,
		},
	}
	if msg.InputTransactionID != "" {
		userMsg.SendTxnID = networkid.RawTransactionID(msg.InputTransactionID)
	}
	if _, err := cc.UserLogin.Bridge.GetGhostByID(ctx, userMsg.SenderID); err != nil {
		cc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to ensure user ghost before saving message")
	}
	if err := cc.UserLogin.Bridge.DB.Message.Insert(ctx, userMsg); err != nil {
		cc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to insert user message")
	}

	if !cc.acquireRoom(roomID) {
		cc.sendPendingStatus(ctx, portal, msg.Event, "Queued — waiting for current turn to finish...")
		cc.queuePendingCodex(roomID, &codexPendingMessage{
			event:  msg.Event,
			portal: portal,
			meta:   meta,
			body:   body,
		})
		return &bridgev2.MatrixMessageResponse{
			DB:      userMsg,
			Pending: true,
		}, nil
	}

	cc.sendPendingStatus(ctx, portal, msg.Event, "Processing...")

	go func() {
		func() {
			defer cc.releaseRoom(roomID)
			cc.runTurn(cc.backgroundContext(ctx), portal, meta, msg.Event, body)
		}()
		cc.processPendingCodex(roomID)
	}()

	return &bridgev2.MatrixMessageResponse{
		DB:      userMsg,
		Pending: true,
	}, nil
}

func (cc *CodexClient) runTurn(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, sourceEvent *event.Event, body string) {
	log := cc.loggerForContext(ctx)
	state := newStreamingState(ctx, meta, sourceEvent.ID, sourceEvent.Sender.String(), portal.MXID)
	state.startedAtMs = time.Now().UnixMilli()

	model := cc.connector.Config.Codex.DefaultModel
	threadID := strings.TrimSpace(meta.CodexThreadID)
	cwd := strings.TrimSpace(meta.CodexCwd)

	// Post placeholder timeline message immediately to get an event id for streaming.
	state.initialEventID = cc.sendInitialStreamMessage(ctx, portal, state, "...", state.turnID)
	if state.initialEventID == "" {
		log.Warn().Msg("Failed to send initial streaming message")
		return
	}
	cc.emitUIStart(ctx, portal, state, model)
	cc.uiEmitter(state).EmitUIStepStart(ctx, portal)

	approvalPolicy := "unlessTrusted"
	if lvl, _ := stringutil.NormalizeElevatedLevel(meta.ElevatedLevel); lvl == "full" {
		approvalPolicy = "never"
	}

	// Start turn.
	var turnStart struct {
		Turn struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"turn"`
	}
	turnStartCtx, cancelTurnStart := context.WithTimeout(ctx, 60*time.Second)
	defer cancelTurnStart()
	err := cc.rpc.Call(turnStartCtx, "turn/start", map[string]any{
		"threadId": threadID,
		"input": []map[string]any{
			{"type": "text", "text": body},
		},
		"cwd":            cwd,
		"approvalPolicy": approvalPolicy,
		"sandboxPolicy":  cc.buildSandboxPolicy(cwd),
	}, &turnStart)
	if err != nil {
		cc.uiEmitter(state).EmitUIError(ctx, portal, err.Error())
		cc.emitUIFinish(ctx, portal, state, model, "failed")
		cc.sendFinalAssistantTurn(ctx, portal, state, model, "failed")
		cc.saveAssistantMessage(ctx, portal, state, model, "failed")
		return
	}
	turnID := strings.TrimSpace(turnStart.Turn.ID)
	if turnID == "" {
		turnID = "turn_unknown"
	}

	turnCh := cc.subscribeTurn(threadID, turnID)
	defer cc.unsubscribeTurn(threadID, turnID)

	cc.activeMu.Lock()
	cc.activeTurns[codexTurnKey(threadID, turnID)] = &codexActiveTurn{
		portal:   portal,
		meta:     meta,
		state:    state,
		threadID: threadID,
		turnID:   turnID,
		model:    model,
	}
	cc.activeMu.Unlock()
	defer func() {
		cc.activeMu.Lock()
		delete(cc.activeTurns, codexTurnKey(threadID, turnID))
		cc.activeMu.Unlock()
	}()

	finishStatus := "completed"
	var completedErr string
	maxWait := time.NewTimer(10 * time.Minute)
	defer maxWait.Stop()
	for {
		select {
		case evt := <-turnCh:
			cc.handleNotif(ctx, portal, meta, state, model, threadID, turnID, evt)
			if st, errText, ok := codexTurnCompletedStatus(evt, threadID, turnID); ok {
				finishStatus = st
				completedErr = errText
				goto done
			}
			maxWait.Reset(10 * time.Minute)
		case <-maxWait.C:
			finishStatus = "timeout"
			goto done
		case <-ctx.Done():
			finishStatus = "interrupted"
			goto done
		}
	}

done:
	log.Debug().Str("status", finishStatus).Str("thread", threadID).Str("turn", turnID).Msg("Codex turn finished")
	state.completedAtMs = time.Now().UnixMilli()
	// If we observed turn-level diff updates, finalize them as a dedicated tool output.
	if diff := strings.TrimSpace(state.codexLatestDiff); diff != "" {
		diffToolID := fmt.Sprintf("diff-%s", turnID)
		cc.ensureUIToolInputStart(ctx, portal, state, diffToolID, "diff", true, map[string]any{"turnId": turnID})
		cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, diffToolID, diff, true, false)
		state.toolCalls = append(state.toolCalls, ToolCallMetadata{
			CallID:        diffToolID,
			ToolName:      "diff",
			ToolType:      string(matrixevents.ToolTypeProvider),
			Input:         map[string]any{"turnId": turnID},
			Output:        map[string]any{"diff": diff},
			Status:        string(matrixevents.ToolStatusCompleted),
			ResultStatus:  string(matrixevents.ResultStatusSuccess),
			StartedAtMs:   state.startedAtMs,
			CompletedAtMs: state.completedAtMs,
		})
	}
	if completedErr != "" {
		cc.uiEmitter(state).EmitUIError(ctx, portal, completedErr)
	}
	cc.emitUIFinish(ctx, portal, state, model, finishStatus)
	cc.sendFinalAssistantTurn(ctx, portal, state, model, finishStatus)
	cc.saveAssistantMessage(ctx, portal, state, model, finishStatus)
	cc.markMessageSendSuccess(ctx, portal, sourceEvent, state)
}

func (cc *CodexClient) appendCodexToolOutput(state *streamingState, toolCallID, delta string) string {
	if state == nil || toolCallID == "" {
		return delta
	}
	if state.codexToolOutputBuffers == nil {
		state.codexToolOutputBuffers = make(map[string]*strings.Builder)
	}
	b := state.codexToolOutputBuffers[toolCallID]
	if b == nil {
		b = &strings.Builder{}
		state.codexToolOutputBuffers[toolCallID] = b
	}
	b.WriteString(delta)
	return b.String()
}

func (cc *CodexClient) handleSimpleOutputDelta(
	ctx context.Context, portal *bridgev2.Portal, state *streamingState,
	params json.RawMessage, threadID, turnID, defaultToolName string,
) {
	var p struct {
		Delta  string `json:"delta"`
		ItemID string `json:"itemId"`
		Thread string `json:"threadId"`
		Turn   string `json:"turnId"`
	}
	_ = json.Unmarshal(params, &p)
	if p.Thread != threadID || p.Turn != turnID {
		return
	}
	toolCallID := strings.TrimSpace(p.ItemID)
	if toolCallID == "" {
		toolCallID = defaultToolName
	}
	buf := cc.appendCodexToolOutput(state, toolCallID, p.Delta)
	cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, toolCallID, buf, true, true)
}

func (cc *CodexClient) handleNotif(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, state *streamingState, model, threadID, turnID string, evt codexNotif) {
	switch evt.Method {
	case "error":
		var p struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(evt.Params, &p)
		if strings.TrimSpace(p.Error.Message) != "" {
			cc.uiEmitter(state).EmitUIError(ctx, portal, p.Error.Message)
			cc.sendSystemNoticeOnce(ctx, portal, state, "turn:error", "Codex error: "+strings.TrimSpace(p.Error.Message))
		}

	case "item/agentMessage/delta":
		var p struct {
			Delta  string `json:"delta"`
			ItemID string `json:"itemId"`
			Thread string `json:"threadId"`
			Turn   string `json:"turnId"`
		}
		_ = json.Unmarshal(evt.Params, &p)
		if p.Thread != threadID || p.Turn != turnID {
			return
		}
		if state.firstToken {
			state.firstToken = false
			state.firstTokenAtMs = time.Now().UnixMilli()
		}
		state.accumulated.WriteString(p.Delta)
		state.visibleAccumulated.WriteString(p.Delta)
		cc.uiEmitter(state).EmitUITextDelta(ctx, portal, p.Delta)

	case "item/reasoning/summaryTextDelta":
		var p struct {
			Delta  string `json:"delta"`
			Thread string `json:"threadId"`
			Turn   string `json:"turnId"`
		}
		_ = json.Unmarshal(evt.Params, &p)
		if p.Thread != threadID || p.Turn != turnID {
			return
		}
		state.codexReasoningSummarySeen = true
		if state.firstToken {
			state.firstToken = false
			state.firstTokenAtMs = time.Now().UnixMilli()
		}
		state.reasoning.WriteString(p.Delta)
		cc.uiEmitter(state).EmitUIReasoningDelta(ctx, portal, p.Delta)

	case "item/reasoning/summaryPartAdded":
		var p struct {
			Thread string `json:"threadId"`
			Turn   string `json:"turnId"`
		}
		_ = json.Unmarshal(evt.Params, &p)
		if p.Thread != threadID || p.Turn != turnID {
			return
		}
		state.codexReasoningSummarySeen = true
		if state.reasoning.Len() > 0 {
			state.reasoning.WriteString("\n")
			cc.uiEmitter(state).EmitUIReasoningDelta(ctx, portal, "\n")
		}

	case "item/reasoning/textDelta":
		var p struct {
			Delta  string `json:"delta"`
			Thread string `json:"threadId"`
			Turn   string `json:"turnId"`
		}
		_ = json.Unmarshal(evt.Params, &p)
		if p.Thread != threadID || p.Turn != turnID {
			return
		}
		// Prefer summary deltas when present to avoid duplicate reasoning output.
		if state.codexReasoningSummarySeen {
			return
		}
		if state.firstToken {
			state.firstToken = false
			state.firstTokenAtMs = time.Now().UnixMilli()
		}
		state.reasoning.WriteString(p.Delta)
		cc.uiEmitter(state).EmitUIReasoningDelta(ctx, portal, p.Delta)

	case "item/commandExecution/outputDelta":
		cc.handleSimpleOutputDelta(ctx, portal, state, evt.Params, threadID, turnID, "commandExecution")

	case "item/fileChange/outputDelta":
		cc.handleSimpleOutputDelta(ctx, portal, state, evt.Params, threadID, turnID, "fileChange")

	case "item/mcpToolCall/outputDelta":
		var p struct {
			Delta  string `json:"delta"`
			ItemID string `json:"itemId"`
			Tool   string `json:"tool"`
			Thread string `json:"threadId"`
			Turn   string `json:"turnId"`
		}
		_ = json.Unmarshal(evt.Params, &p)
		if p.Thread != threadID || p.Turn != turnID {
			return
		}
		toolCallID := strings.TrimSpace(p.ItemID)
		toolName := strings.TrimSpace(p.Tool)
		if toolName == "" {
			toolName = "mcpToolCall"
		}
		if toolCallID == "" {
			toolCallID = toolName
		}
		buf := cc.appendCodexToolOutput(state, toolCallID, p.Delta)
		cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, toolCallID, buf, true, true)

	case "item/collabToolCall/outputDelta":
		cc.handleSimpleOutputDelta(ctx, portal, state, evt.Params, threadID, turnID, "collabToolCall")

	case "turn/diff/updated":
		var p struct {
			Thread string `json:"threadId"`
			Turn   string `json:"turnId"`
			Diff   string `json:"diff"`
		}
		_ = json.Unmarshal(evt.Params, &p)
		if p.Thread != threadID || p.Turn != turnID {
			return
		}
		state.codexLatestDiff = p.Diff
		diffToolID := fmt.Sprintf("diff-%s", turnID)
		cc.ensureUIToolInputStart(ctx, portal, state, diffToolID, "diff", true, map[string]any{"turnId": turnID})
		cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, diffToolID, p.Diff, true, true)

	case "item/plan/delta":
		cc.handleSimpleOutputDelta(ctx, portal, state, evt.Params, threadID, turnID, "plan")

	case "turn/plan/updated":
		var p struct {
			Thread      string           `json:"threadId"`
			Turn        string           `json:"turnId"`
			Explanation *string          `json:"explanation"`
			Plan        []map[string]any `json:"plan"`
		}
		_ = json.Unmarshal(evt.Params, &p)
		if p.Thread != threadID || p.Turn != turnID {
			return
		}
		toolCallID := fmt.Sprintf("turn-plan-%s", turnID)
		input := map[string]any{}
		if p.Explanation != nil && strings.TrimSpace(*p.Explanation) != "" {
			input["explanation"] = strings.TrimSpace(*p.Explanation)
		}
		cc.ensureUIToolInputStart(ctx, portal, state, toolCallID, "plan", true, input)
		cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, toolCallID, map[string]any{
			"explanation": input["explanation"],
			"plan":        p.Plan,
		}, true, true)
		cc.sendSystemNoticeOnce(ctx, portal, state, "turn:plan_updated", "Codex updated the plan.")

	case "thread/tokenUsage/updated":
		var p struct {
			Thread     string `json:"threadId"`
			Turn       string `json:"turnId"`
			TokenUsage struct {
				Total struct {
					InputTokens           int64 `json:"inputTokens"`
					CachedInputTokens     int64 `json:"cachedInputTokens"`
					OutputTokens          int64 `json:"outputTokens"`
					ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
					TotalTokens           int64 `json:"totalTokens"`
				} `json:"total"`
			} `json:"tokenUsage"`
		}
		_ = json.Unmarshal(evt.Params, &p)
		if p.Thread != threadID || p.Turn != turnID {
			return
		}
		state.promptTokens = p.TokenUsage.Total.InputTokens + p.TokenUsage.Total.CachedInputTokens
		state.completionTokens = p.TokenUsage.Total.OutputTokens
		state.reasoningTokens = p.TokenUsage.Total.ReasoningOutputTokens
		state.totalTokens = p.TokenUsage.Total.TotalTokens
		cc.uiEmitter(state).EmitUIMessageMetadata(ctx, portal, cc.buildUIMessageMetadata(state, model, true, ""))

	case "item/started":
		var p struct {
			Thread string          `json:"threadId"`
			Turn   string          `json:"turnId"`
			Item   json.RawMessage `json:"item"`
		}
		_ = json.Unmarshal(evt.Params, &p)
		if p.Thread != threadID || p.Turn != turnID {
			return
		}
		cc.handleItemStarted(ctx, portal, state, p.Item)

	case "item/completed":
		var p struct {
			Thread string          `json:"threadId"`
			Turn   string          `json:"turnId"`
			Item   json.RawMessage `json:"item"`
		}
		_ = json.Unmarshal(evt.Params, &p)
		if p.Thread != threadID || p.Turn != turnID {
			return
		}
		cc.handleItemCompleted(ctx, portal, state, p.Item)
	}
}

func codexTurnCompletedStatus(evt codexNotif, threadID, turnID string) (status string, errText string, ok bool) {
	if evt.Method != "turn/completed" {
		return "", "", false
	}
	var p struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Turn     struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
	}
	_ = json.Unmarshal(evt.Params, &p)
	if tid := strings.TrimSpace(p.ThreadID); tid != "" && tid != threadID {
		return "", "", false
	}
	if tid := strings.TrimSpace(p.TurnID); tid != "" && tid != turnID {
		return "", "", false
	}
	if tid := strings.TrimSpace(p.Turn.ID); tid != "" && tid != turnID {
		return "", "", false
	}
	status = strings.TrimSpace(p.Turn.Status)
	if status == "" {
		status = "completed"
	}
	if p.Turn.Error != nil {
		errText = strings.TrimSpace(p.Turn.Error.Message)
	}
	return status, errText, true
}

func (cc *CodexClient) handleItemStarted(ctx context.Context, portal *bridgev2.Portal, state *streamingState, raw json.RawMessage) {
	var probe struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	_ = json.Unmarshal(raw, &probe)
	itemID := strings.TrimSpace(probe.ID)
	switch probe.Type {
	case "agentMessage":
		// Streaming comes via item/agentMessage/delta; avoid duplicating.
		return
	case "reasoning":
		// Stream deltas via item/reasoning/*; item completion will backfill if deltas are absent.
		return
	case "commandExecution":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.ensureUIToolInputStart(ctx, portal, state, itemID, "commandExecution", true, it)
	case "fileChange":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.ensureUIToolInputStart(ctx, portal, state, itemID, "fileChange", true, it)
	case "mcpToolCall":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		toolName, _ := it["tool"].(string)
		if strings.TrimSpace(toolName) == "" {
			toolName = "mcpToolCall"
		}
		cc.ensureUIToolInputStart(ctx, portal, state, itemID, toolName, true, it)
	case "collabToolCall":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.ensureUIToolInputStart(ctx, portal, state, itemID, "collabToolCall", true, it)
	case "webSearch":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.ensureUIToolInputStart(ctx, portal, state, itemID, "webSearch", true, it)
		notice := "Codex started web search."
		if q, ok := it["query"].(string); ok && strings.TrimSpace(q) != "" {
			notice = fmt.Sprintf("Codex started web search: %s", strings.TrimSpace(q))
		}
		cc.sendSystemNoticeOnce(ctx, portal, state, "websearch:"+itemID, notice)
	case "imageView":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.ensureUIToolInputStart(ctx, portal, state, itemID, "imageView", true, it)
		cc.sendSystemNoticeOnce(ctx, portal, state, "imageview:"+itemID, "Codex viewed an image.")
	case "plan":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.ensureUIToolInputStart(ctx, portal, state, itemID, "plan", true, it)
	case "enteredReviewMode", "exitedReviewMode":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.ensureUIToolInputStart(ctx, portal, state, itemID, "review", true, it)
		if probe.Type == "enteredReviewMode" {
			cc.sendSystemNoticeOnce(ctx, portal, state, "review:entered:"+itemID, "Codex entered review mode.")
		} else {
			cc.sendSystemNoticeOnce(ctx, portal, state, "review:exited:"+itemID, "Codex exited review mode.")
		}
	case "contextCompaction":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.ensureUIToolInputStart(ctx, portal, state, itemID, "contextCompaction", true, it)
		cc.sendSystemNoticeOnce(ctx, portal, state, "compaction:started:"+itemID, "Codex is compacting context…")
	}
}

func newProviderToolCall(id, name string, output map[string]any) ToolCallMetadata {
	now := time.Now().UnixMilli()
	return ToolCallMetadata{
		CallID:        id,
		ToolName:      name,
		ToolType:      string(matrixevents.ToolTypeProvider),
		Output:        output,
		Status:        string(matrixevents.ToolStatusCompleted),
		ResultStatus:  string(matrixevents.ResultStatusSuccess),
		StartedAtMs:   now,
		CompletedAtMs: now,
	}
}

func (cc *CodexClient) handleItemCompleted(ctx context.Context, portal *bridgev2.Portal, state *streamingState, raw json.RawMessage) {
	var probe struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	_ = json.Unmarshal(raw, &probe)
	itemID := strings.TrimSpace(probe.ID)
	switch probe.Type {
	case "agentMessage":
		// If delta events were dropped, backfill once from the completed item.
		if state != nil && strings.TrimSpace(state.accumulated.String()) != "" {
			return
		}
		var it struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &it)
		if strings.TrimSpace(it.Text) == "" {
			return
		}
		state.accumulated.WriteString(it.Text)
		state.visibleAccumulated.WriteString(it.Text)
		cc.uiEmitter(state).EmitUITextDelta(ctx, portal, it.Text)
		return
	case "reasoning":
		// If reasoning deltas were dropped, backfill once from the completed item.
		if state != nil && strings.TrimSpace(state.reasoning.String()) != "" {
			return
		}
		var it struct {
			Summary []string `json:"summary"`
			Content []string `json:"content"`
		}
		_ = json.Unmarshal(raw, &it)
		var text string
		if len(it.Summary) > 0 {
			text = strings.Join(it.Summary, "\n")
		} else if len(it.Content) > 0 {
			text = strings.Join(it.Content, "\n")
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		state.reasoning.WriteString(text)
		cc.uiEmitter(state).EmitUIReasoningDelta(ctx, portal, text)
		return
	case "commandExecution", "fileChange", "mcpToolCall":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		statusVal, _ := it["status"].(string)
		statusVal = strings.TrimSpace(statusVal)
		switch statusVal {
		case "declined":
			cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, map[string]any{
				"type":       "tool-output-denied",
				"toolCallId": itemID,
			}, true, false)
		case "failed":
			errText := "tool failed"
			if errObj, ok := it["error"].(map[string]any); ok {
				if msg, ok := errObj["message"].(string); ok && strings.TrimSpace(msg) != "" {
					errText = strings.TrimSpace(msg)
				}
			}
			cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, map[string]any{
				"type":             "tool-output-error",
				"toolCallId":       itemID,
				"errorText":        errText,
				"providerExecuted": true,
			}, true, false)
		default:
			cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, it, true, false)
		}

		tc := newProviderToolCall(itemID, fmt.Sprintf("%v", it["type"]), it)
		switch statusVal {
		case "declined":
			tc.ResultStatus = string(matrixevents.ResultStatusDenied)
			tc.ErrorMessage = "Denied by user"
		case "failed":
			tc.ResultStatus = string(matrixevents.ResultStatusError)
			if errObj, ok := it["error"].(map[string]any); ok {
				if msg, ok := errObj["message"].(string); ok && strings.TrimSpace(msg) != "" {
					tc.ErrorMessage = strings.TrimSpace(msg)
				}
			}
		default:
			tc.ResultStatus = string(matrixevents.ResultStatusSuccess)
		}
		state.toolCalls = append(state.toolCalls, tc)
	case "collabToolCall":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, it, true, false)
		state.toolCalls = append(state.toolCalls, newProviderToolCall(itemID, "collabToolCall", it))
	case "webSearch":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, it, true, false)
		state.toolCalls = append(state.toolCalls, newProviderToolCall(itemID, "webSearch", it))
		// Extract web search citations and emit source-url stream events.
		if outputJSON, err := json.Marshal(it); err == nil {
			collectToolOutputCitations(state, "webSearch", string(outputJSON))
			for _, citation := range state.sourceCitations {
				cc.uiEmitter(state).EmitUISourceURL(ctx, portal, citation)
			}
		}
	case "imageView":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, it, true, false)
		state.toolCalls = append(state.toolCalls, newProviderToolCall(itemID, "imageView", it))
	case "plan":
		var it struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &it)
		text := strings.TrimSpace(it.Text)
		if text == "" {
			return
		}
		cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, text, true, false)
		state.toolCalls = append(state.toolCalls, newProviderToolCall(itemID, "plan", map[string]any{"text": text}))
	case "enteredReviewMode":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, it, true, false)
		state.toolCalls = append(state.toolCalls, newProviderToolCall(itemID, "review", it))
	case "exitedReviewMode":
		var it struct {
			Review string `json:"review"`
		}
		_ = json.Unmarshal(raw, &it)
		text := strings.TrimSpace(it.Review)
		if text == "" {
			return
		}
		cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, text, true, false)
		state.toolCalls = append(state.toolCalls, newProviderToolCall(itemID, "review", map[string]any{"review": text}))
	case "contextCompaction":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, it, true, false)
		state.toolCalls = append(state.toolCalls, newProviderToolCall(itemID, "contextCompaction", it))
		cc.sendSystemNoticeOnce(ctx, portal, state, "compaction:completed:"+itemID, "Codex finished compacting context.")
	}
}

func (cc *CodexClient) ensureRPC(ctx context.Context) error {
	cc.rpcMu.Lock()
	defer cc.rpcMu.Unlock()
	if cc.rpc != nil {
		return nil
	}

	// New app-server process => previously loaded thread ids are no longer in memory.
	cc.loadedMu.Lock()
	cc.loadedThreads = make(map[string]bool)
	cc.loadedMu.Unlock()

	meta := loginMetadata(cc.UserLogin)
	cmd := cc.resolveCodexCommand(meta)
	if _, err := exec.LookPath(cmd); err != nil {
		return err
	}
	codexHome := strings.TrimSpace(meta.CodexHome)
	if codexHome == "" {
		return errors.New("missing CODEX_HOME")
	}
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return err
	}
	launch, err := cc.connector.resolveAppServerLaunch()
	if err != nil {
		return err
	}
	rpc, err := codexrpc.StartProcess(ctx, codexrpc.ProcessConfig{
		Command:      cmd,
		Args:         launch.Args,
		Env:          []string{"CODEX_HOME=" + codexHome},
		WebSocketURL: launch.WebSocketURL,
	})
	if err != nil {
		return err
	}
	cc.rpc = rpc

	initCtx, cancelInit := context.WithTimeout(ctx, 45*time.Second)
	defer cancelInit()
	ci := cc.connector.Config.Codex.ClientInfo
	_, err = rpc.Initialize(initCtx, codexrpc.ClientInfo{Name: ci.Name, Title: ci.Title, Version: ci.Version}, false)
	if err != nil {
		_ = rpc.Close()
		cc.rpc = nil
		return err
	}

	cc.dispatchOnce.Do(func() {
		go cc.dispatchNotifications()
	})

	rpc.OnNotification(func(method string, params json.RawMessage) {
		if !cc.loggedIn.Load() {
			return
		}
		select {
		case cc.notifCh <- codexNotif{Method: method, Params: params}:
		default:
		}
	})

	// Approval requests.
	rpc.HandleRequest("item/commandExecution/requestApproval", cc.handleCommandApprovalRequest)
	rpc.HandleRequest("item/fileChange/requestApproval", cc.handleFileChangeApprovalRequest)

	return nil
}

func (cc *CodexClient) subscribeTurn(threadID, turnID string) chan codexNotif {
	key := codexTurnKey(threadID, turnID)
	ch := make(chan codexNotif, 4096)
	cc.subMu.Lock()
	cc.turnSubs[key] = ch
	cc.subMu.Unlock()
	return ch
}

func (cc *CodexClient) unsubscribeTurn(threadID, turnID string) {
	key := codexTurnKey(threadID, turnID)
	cc.subMu.Lock()
	delete(cc.turnSubs, key)
	cc.subMu.Unlock()
}

func codexExtractThreadTurn(params json.RawMessage) (threadID, turnID string, ok bool) {
	var p struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "", "", false
	}
	threadID = strings.TrimSpace(p.ThreadID)
	turnID = strings.TrimSpace(p.TurnID)
	return threadID, turnID, threadID != "" && turnID != ""
}

func (cc *CodexClient) dispatchNotifications() {
	for {
		var evt codexNotif
		select {
		case <-cc.notifDone:
			return
		case e, ok := <-cc.notifCh:
			if !ok {
				return
			}
			evt = e
		}
		// Track logged-in state if Codex emits these (best-effort).
		if evt.Method == "account/updated" {
			var p struct {
				AuthMode *string `json:"authMode"`
			}
			_ = json.Unmarshal(evt.Params, &p)
			cc.loggedIn.Store(p.AuthMode != nil && strings.TrimSpace(*p.AuthMode) != "")
			continue
		}

		threadID, turnID, ok := codexExtractThreadTurn(evt.Params)
		if !ok {
			continue
		}
		if evt.Method == "turn/completed" || evt.Method == "error" {
			cc.log.Debug().Str("method", evt.Method).Str("thread", threadID).Str("turn", turnID).
				Msg("Codex terminal notification")
		}
		key := codexTurnKey(threadID, turnID)

		cc.subMu.Lock()
		ch := cc.turnSubs[key]
		cc.subMu.Unlock()
		if ch == nil {
			// Race: turn/start just returned but subscribeTurn() hasn't registered yet.
			// Spin-wait briefly for terminal events that must not be dropped.
			if evt.Method == "turn/completed" || evt.Method == "error" {
				for i := 0; i < 20; i++ {
					time.Sleep(50 * time.Millisecond)
					cc.subMu.Lock()
					ch = cc.turnSubs[key]
					cc.subMu.Unlock()
					if ch != nil {
						break
					}
				}
			}
			if ch == nil {
				continue
			}
		}

		// Try non-blocking, but ensure critical terminal events are delivered.
		select {
		case ch <- evt:
		default:
			if evt.Method == "turn/completed" || evt.Method == "error" {
				select {
				case ch <- evt:
				case <-time.After(2 * time.Second):
				}
			}
		}
	}
}

func (cc *CodexClient) resolveCodexCommand(meta *UserLoginMetadata) string {
	if meta != nil {
		if v := strings.TrimSpace(meta.CodexCommand); v != "" {
			return v
		}
	}
	if cc.connector != nil && cc.connector.Config.Codex != nil {
		if v := strings.TrimSpace(cc.connector.Config.Codex.Command); v != "" {
			return v
		}
	}
	return "codex"
}

func (cc *CodexClient) codexNetworkAccess() bool {
	if cc.connector == nil || cc.connector.Config.Codex == nil || cc.connector.Config.Codex.NetworkAccess == nil {
		return true
	}
	return *cc.connector.Config.Codex.NetworkAccess
}

func (cc *CodexClient) backgroundContext(ctx context.Context) context.Context {
	base := context.Background()
	if cc.UserLogin != nil && cc.UserLogin.Bridge != nil && cc.UserLogin.Bridge.BackgroundCtx != nil {
		base = cc.UserLogin.Bridge.BackgroundCtx
	}
	return cc.loggerForContext(ctx).WithContext(base)
}

func (cc *CodexClient) ensureDefaultCodexChat(ctx context.Context) error {
	portalKey := defaultCodexChatPortalKey(cc.UserLogin.ID)
	portal, err := cc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return err
	}
	if portal.Metadata == nil {
		portal.Metadata = &PortalMetadata{}
	}
	meta := portalMeta(portal)
	meta.IsCodexRoom = true
	if meta.Title == "" {
		meta.Title = "Codex"
	}
	if meta.Slug == "" {
		meta.Slug = "codex"
	}
	portal.RoomType = database.RoomTypeDM
	portal.OtherUserID = codexGhostID
	portal.Name = meta.Title
	portal.NameSet = true
	if err := portal.Save(ctx); err != nil {
		return err
	}

	if portal.MXID == "" {
		info := cc.composeCodexChatInfo(meta.Title)
		if err := portal.CreateMatrixRoom(ctx, cc.UserLogin, info); err != nil {
			return err
		}
		cc.sendSystemNotice(ctx, portal, "AI Chats can make mistakes.")
	}

	// Ensure thread started at a temp dir.
	return cc.ensureCodexThread(ctx, portal, meta)
}

func (cc *CodexClient) composeCodexChatInfo(title string) *bridgev2.ChatInfo {
	if title == "" {
		title = "Codex"
	}
	members := bridgev2.ChatMemberMap{
		humanUserID(cc.UserLogin.ID): {
			EventSender: bridgev2.EventSender{
				IsFromMe:    true,
				SenderLogin: cc.UserLogin.ID,
			},
			Membership: event.MembershipJoin,
		},
		codexGhostID: {
			EventSender: bridgev2.EventSender{
				Sender:      codexGhostID,
				SenderLogin: cc.UserLogin.ID,
			},
			Membership: event.MembershipJoin,
			UserInfo: &bridgev2.UserInfo{
				Name:  ptr.Ptr("Codex"),
				IsBot: ptr.Ptr(true),
			},
			MemberEventExtra: map[string]any{
				"displayname": "Codex",
			},
		},
	}
	return &bridgev2.ChatInfo{
		Name: ptr.Ptr(title),
		Type: ptr.Ptr(database.RoomTypeDM),
		Members: &bridgev2.ChatMemberList{
			IsFull:      true,
			OtherUserID: codexGhostID,
			MemberMap:   members,
			PowerLevels: &bridgev2.PowerLevelOverrides{
				Events: map[event.Type]int{
					matrixevents.RoomCapabilitiesEventType: 100,
					matrixevents.RoomSettingsEventType:     0,
				},
			},
		},
	}
}

func (cc *CodexClient) buildSandboxPolicy(cwd string) map[string]any {
	return map[string]any{
		"type":          "workspaceWrite",
		"writableRoots": []string{cwd},
		"networkAccess": cc.codexNetworkAccess(),
	}
}

func (cc *CodexClient) ensureCodexThread(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) error {
	if meta == nil || portal == nil {
		return errors.New("missing portal/meta")
	}
	if strings.TrimSpace(meta.CodexCwd) == "" {
		cwd, err := os.MkdirTemp("", "ai-bridge-codex-*")
		if err != nil {
			return err
		}
		meta.CodexCwd = cwd
	}
	if _, err := os.Stat(meta.CodexCwd); err != nil {
		cwd, mkErr := os.MkdirTemp("", "ai-bridge-codex-*")
		if mkErr != nil {
			return mkErr
		}
		meta.CodexCwd = cwd
		meta.CodexThreadID = ""
	}
	if err := portal.Save(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(meta.CodexThreadID) != "" {
		return cc.ensureCodexThreadLoaded(ctx, portal, meta)
	}
	if err := cc.ensureRPC(ctx); err != nil {
		return err
	}
	model := cc.connector.Config.Codex.DefaultModel
	var resp struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	callCtx, cancelCall := context.WithTimeout(ctx, 60*time.Second)
	defer cancelCall()
	err := cc.rpc.Call(callCtx, "thread/start", map[string]any{
		"model":          model,
		"cwd":            meta.CodexCwd,
		"approvalPolicy": "unlessTrusted",
		"sandboxPolicy":  cc.buildSandboxPolicy(meta.CodexCwd),
	}, &resp)
	if err != nil {
		return err
	}
	meta.CodexThreadID = strings.TrimSpace(resp.Thread.ID)
	if meta.CodexThreadID == "" {
		return errors.New("codex returned empty thread id")
	}
	if err := portal.Save(ctx); err != nil {
		return err
	}
	cc.loadedMu.Lock()
	cc.loadedThreads[meta.CodexThreadID] = true
	cc.loadedMu.Unlock()
	return nil
}

func (cc *CodexClient) ensureCodexThreadLoaded(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) error {
	if meta == nil {
		return errors.New("missing metadata")
	}
	threadID := strings.TrimSpace(meta.CodexThreadID)
	if threadID == "" {
		return errors.New("missing thread id")
	}
	cc.loadedMu.Lock()
	loaded := cc.loadedThreads[threadID]
	cc.loadedMu.Unlock()
	if loaded {
		return nil
	}
	if err := cc.ensureRPC(ctx); err != nil {
		return err
	}
	var resp struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	callCtx, cancelCall := context.WithTimeout(ctx, 60*time.Second)
	defer cancelCall()
	err := cc.rpc.Call(callCtx, "thread/resume", map[string]any{
		"threadId":       threadID,
		"model":          cc.connector.Config.Codex.DefaultModel,
		"cwd":            meta.CodexCwd,
		"approvalPolicy": "unlessTrusted",
		"sandboxPolicy":  cc.buildSandboxPolicy(meta.CodexCwd),
	}, &resp)
	if err != nil {
		// If the stored thread can't be resumed (missing/corrupt), fall back to a fresh thread.
		meta.CodexThreadID = ""
		if err2 := portal.Save(ctx); err2 != nil {
			return err2
		}
		return cc.ensureCodexThread(ctx, portal, meta)
	}
	cc.loadedMu.Lock()
	cc.loadedThreads[threadID] = true
	cc.loadedMu.Unlock()
	return nil
}

// HandleMatrixDeleteChat best-effort archives the Codex thread and removes the temp cwd.
// The core bridge handles Matrix-side room cleanup separately.
func (cc *CodexClient) HandleMatrixDeleteChat(ctx context.Context, msg *bridgev2.MatrixDeleteChat) error {
	if msg == nil || msg.Portal == nil {
		return nil
	}
	meta := portalMeta(msg.Portal)
	if meta == nil || !meta.IsCodexRoom {
		return nil
	}
	if err := cc.ensureRPC(ctx); err != nil {
		return nil
	}

	// If a turn is in-flight for this thread, try to interrupt it.
	tid := strings.TrimSpace(meta.CodexThreadID)
	cc.activeMu.Lock()
	var active *codexActiveTurn
	for _, at := range cc.activeTurns {
		if at != nil && strings.TrimSpace(at.threadID) == tid {
			active = at
			break
		}
	}
	cc.activeMu.Unlock()
	if active != nil && strings.TrimSpace(active.threadID) == tid {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_ = cc.rpc.Call(callCtx, "turn/interrupt", map[string]any{
			"threadId": active.threadID,
			"turnId":   active.turnID,
		}, &struct{}{})
		cancel()
	}

	if tid != "" {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_ = cc.rpc.Call(callCtx, "thread/archive", map[string]any{"threadId": tid}, &struct{}{})
		cancel()
		cc.loadedMu.Lock()
		delete(cc.loadedThreads, tid)
		cc.loadedMu.Unlock()
	}
	if cwd := strings.TrimSpace(meta.CodexCwd); cwd != "" {
		_ = os.RemoveAll(cwd)
	}
	meta.CodexThreadID = ""
	meta.CodexCwd = ""
	_ = msg.Portal.Save(ctx)
	return nil
}

func (cc *CodexClient) sendSystemNotice(ctx context.Context, portal *bridgev2.Portal, message string) {
	if portal == nil || portal.MXID == "" || cc.UserLogin == nil || cc.UserLogin.Bridge == nil {
		return
	}
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:   networkid.PartID("0"),
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				MsgType:  event.MsgNotice,
				Body:     strings.TrimSpace(message),
				Mentions: &event.Mentions{},
			},
		}},
	}
	bg := cc.backgroundContext(ctx)
	sendCtx, cancel := context.WithTimeout(bg, 10*time.Second)
	defer cancel()
	cc.sendViaPortal(sendCtx, portal, converted, "")
}

func (cc *CodexClient) sendToast(ctx context.Context, portal *bridgev2.Portal, text string, toastType aiToastType) {
	if portal == nil || portal.MXID == "" || cc.UserLogin == nil || cc.UserLogin.Bridge == nil {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	raw := map[string]any{
		"msgtype": event.MsgNotice,
		"body":    text,
		"com.beeper.ai.toast": map[string]any{
			"text": text,
			"type": string(toastType),
		},
		"m.mentions": map[string]any{},
	}
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:    networkid.PartID("0"),
			Type:  event.EventMessage,
			Extra: raw,
		}},
	}
	bg := cc.backgroundContext(ctx)
	sendCtx, cancel := context.WithTimeout(bg, 10*time.Second)
	defer cancel()
	cc.sendViaPortal(sendCtx, portal, converted, "")
}

func (cc *CodexClient) sendPendingStatus(ctx context.Context, portal *bridgev2.Portal, evt *event.Event, message string) {
	if portal == nil || portal.Bridge == nil || evt == nil {
		return
	}
	st := bridgev2.MessageStatus{
		Status:    event.MessageStatusPending,
		Message:   message,
		IsCertain: true,
	}
	portal.Bridge.Matrix.SendMessageStatus(ctx, &st, bridgev2.StatusEventInfoFromEvent(evt))
}

func (cc *CodexClient) markMessageSendSuccess(ctx context.Context, portal *bridgev2.Portal, evt *event.Event, state *streamingState) {
	if portal == nil || portal.Bridge == nil || evt == nil || state == nil {
		return
	}
	st := bridgev2.MessageStatus{Status: event.MessageStatusSuccess, IsCertain: true}
	portal.Bridge.Matrix.SendMessageStatus(ctx, &st, bridgev2.StatusEventInfoFromEvent(evt))
}

func (cc *CodexClient) acquireRoom(roomID id.RoomID) bool {
	cc.roomMu.Lock()
	defer cc.roomMu.Unlock()
	if cc.activeRooms[roomID] {
		return false
	}
	cc.activeRooms[roomID] = true
	return true
}

func (cc *CodexClient) releaseRoom(roomID id.RoomID) {
	cc.roomMu.Lock()
	defer cc.roomMu.Unlock()
	delete(cc.activeRooms, roomID)
}

func (cc *CodexClient) queuePendingCodex(roomID id.RoomID, pm *codexPendingMessage) {
	cc.roomMu.Lock()
	defer cc.roomMu.Unlock()
	cc.pendingMessages[roomID] = pm
}

func (cc *CodexClient) popPendingCodex(roomID id.RoomID) *codexPendingMessage {
	cc.roomMu.Lock()
	defer cc.roomMu.Unlock()
	pm := cc.pendingMessages[roomID]
	delete(cc.pendingMessages, roomID)
	return pm
}

func (cc *CodexClient) processPendingCodex(roomID id.RoomID) {
	// Peek — don't remove yet so the message isn't lost on transient failures.
	cc.roomMu.Lock()
	pm := cc.pendingMessages[roomID]
	cc.roomMu.Unlock()
	if pm == nil {
		return
	}
	ctx := cc.backgroundContext(context.Background())
	if err := cc.ensureRPC(ctx); err != nil {
		cc.log.Warn().Err(err).Stringer("room", roomID).Msg("Pending codex message: RPC unavailable")
		return
	}
	meta := portalMeta(pm.portal)
	if meta == nil {
		// Bad portal — discard.
		cc.popPendingCodex(roomID)
		return
	}
	if err := cc.ensureCodexThreadLoaded(ctx, pm.portal, meta); err != nil {
		cc.log.Warn().Err(err).Stringer("room", roomID).Msg("Pending codex message: thread load failed")
		return
	}
	if !cc.acquireRoom(roomID) {
		return
	}
	// Committed — now pop.
	cc.popPendingCodex(roomID)
	go func() {
		func() {
			defer cc.releaseRoom(roomID)
			cc.runTurn(ctx, pm.portal, meta, pm.event, pm.body)
		}()
		cc.processPendingCodex(roomID)
	}()
}

// Streaming helpers (Codex -> Matrix AI SDK chunk mapping)

func (cc *CodexClient) sendInitialStreamMessage(ctx context.Context, portal *bridgev2.Portal, state *streamingState, content string, turnID string) id.EventID {
	uiMessage := map[string]any{
		"id":   turnID,
		"role": "assistant",
		"metadata": map[string]any{
			"turn_id": turnID,
		},
		"parts": []any{},
	}

	eventRaw := map[string]any{
		"msgtype":                event.MsgText,
		"body":                   content,
		matrixevents.BeeperAIKey: uiMessage,
		"m.mentions":             map[string]any{},
	}

	msgID := newMessageID()
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:         networkid.PartID("0"),
			Type:       event.EventMessage,
			Content:    &event.MessageEventContent{MsgType: event.MsgText, Body: content},
			Extra:      eventRaw,
			DBMetadata: &MessageMetadata{Role: "assistant", TurnID: turnID},
		}},
	}

	eventID, _, err := cc.sendViaPortal(ctx, portal, converted, msgID)
	if err != nil {
		cc.loggerForContext(ctx).Error().Err(err).Msg("Failed to send initial streaming message")
		return ""
	}
	if state != nil {
		state.networkMessageID = msgID
	}
	cc.loggerForContext(ctx).Info().Stringer("event_id", eventID).Str("turn_id", turnID).Msg("Initial streaming message sent")
	return eventID
}

func (cc *CodexClient) buildUIMessageMetadata(state *streamingState, model string, includeUsage bool, finishReason string) map[string]any {
	return msgconv.BuildUIMessageMetadata(msgconv.UIMessageMetadataParams{
		TurnID:           state.turnID,
		AgentID:          state.agentID,
		Model:            strings.TrimSpace(model),
		FinishReason:     finishReason,
		PromptTokens:     state.promptTokens,
		CompletionTokens: state.completionTokens,
		ReasoningTokens:  state.reasoningTokens,
		TotalTokens:      state.totalTokens,
		StartedAtMs:      state.startedAtMs,
		FirstTokenAtMs:   state.firstTokenAtMs,
		CompletedAtMs:    state.completedAtMs,
		IncludeUsage:     includeUsage,
	})
}

func (cc *CodexClient) emitUIStart(ctx context.Context, portal *bridgev2.Portal, state *streamingState, model string) {
	cc.uiEmitter(state).EmitUIStart(ctx, portal, cc.buildUIMessageMetadata(state, model, false, ""))
}

func (cc *CodexClient) ensureUIToolInputStart(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID, toolName string, providerExecuted bool, input any) {
	if toolCallID == "" {
		return
	}
	ui := cc.uiEmitter(state)
	ui.EnsureUIToolInputStart(ctx, portal, toolCallID, toolName, providerExecuted, false, streamui.ToolDisplayTitle(toolName), nil)
	ui.EmitUIToolInputAvailable(ctx, portal, toolCallID, toolName, input, providerExecuted)
}

func (cc *CodexClient) emitUIToolApprovalRequest(
	ctx context.Context, portal *bridgev2.Portal, state *streamingState,
	approvalID, toolCallID, toolName string, ttlSeconds int,
) {
	cc.uiEmitter(state).EmitUIToolApprovalRequest(ctx, portal, approvalID, toolCallID, toolName, ttlSeconds)
}

// sendToolCallApprovalEvent sends a tool_call timeline event with status "approval_required"
// so the desktop timeline can show inline approval buttons (mirrors AIClient.sendToolCallApprovalEvent).
func (cc *CodexClient) sendToolCallApprovalEvent(
	ctx context.Context, portal *bridgev2.Portal, state *streamingState,
	toolCallID, toolName, approvalID string, expiresAtMs int64,
) {
	if portal == nil || portal.MXID == "" || state == nil {
		return
	}
	if state.suppressSend {
		return
	}
	displayTitle := toolDisplayTitle(toolName)
	toolType := string(matrixevents.ToolTypeProvider)
	if tt, ok := state.ui.UIToolTypeByToolCallID[toolCallID]; ok {
		toolType = string(tt)
	}
	toolCallData := map[string]any{
		"call_id":                toolCallID,
		"turn_id":                state.turnID,
		"tool_name":              toolName,
		"tool_type":              toolType,
		"status":                 string(matrixevents.ToolStatusApprovalRequired),
		"approval_id":            approvalID,
		"approval_expires_at_ms": expiresAtMs,
		"display": map[string]any{
			"title":     displayTitle,
			"collapsed": false,
		},
	}
	eventRaw := map[string]any{
		"body":                           fmt.Sprintf("Approval required for %s", displayTitle),
		"msgtype":                        event.MsgNotice,
		matrixevents.BeeperAIToolCallKey: toolCallData,
	}
	if state.initialEventID != "" {
		eventRaw["m.relates_to"] = map[string]any{
			"rel_type": matrixevents.RelReference,
			"event_id": state.initialEventID.String(),
		}
	}

	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:    networkid.PartID("0"),
			Type:  matrixevents.ToolCallEventType,
			Extra: eventRaw,
		}},
	}

	eventID, _, err := cc.sendViaPortal(ctx, portal, converted, "")
	if err != nil {
		cc.loggerForContext(ctx).Warn().Err(err).
			Str("tool", toolName).Str("approval_id", approvalID).
			Msg("Failed to send tool call approval event")
		return
	}
	cc.loggerForContext(ctx).Debug().
		Stringer("event_id", eventID).
		Str("call_id", toolCallID).
		Str("tool", toolName).
		Str("approval_id", approvalID).
		Msg("Sent tool call approval_required timeline event")
}

// sendActionHintsApprovalEvent sends a timeline message with com.beeper.action_hints
// containing Allow/Always/Deny buttons for tool approval (MSC1485 pattern).
func (cc *CodexClient) sendActionHintsApprovalEvent(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	toolCallID string,
	toolName string,
	approvalID string,
	expiresAtMs int64,
) {
	if portal == nil || portal.MXID == "" {
		return
	}

	var ownerMXID id.UserID
	if cc.UserLogin != nil {
		ownerMXID = cc.UserLogin.UserMXID
	}

	hints := bridgeadapter.BuildApprovalHints(bridgeadapter.ApprovalHintsParams{
		ApprovalID:  approvalID,
		ToolCallID:  toolCallID,
		ToolName:    toolName,
		OwnerMXID:   ownerMXID,
		ExpiresAtMs: expiresAtMs,
	})

	body := fmt.Sprintf("Allow %s tool?", toolName)
	uiMessage := map[string]any{
		"id":   "approval:" + approvalID,
		"role": "assistant",
		"parts": []map[string]any{
			{
				"type":       "action-hints",
				"toolCallId": toolCallID,
				"toolName":   toolName,
			},
		},
	}
	if state != nil && state.turnID != "" {
		uiMessage["metadata"] = map[string]any{"turn_id": state.turnID}
	}

	eventRaw := map[string]any{
		"msgtype":                         event.MsgNotice,
		"body":                            body,
		matrixevents.BeeperAIKey:          uiMessage,
		matrixevents.BeeperActionHintsKey: hints,
		"m.mentions":                      map[string]any{},
	}

	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:    networkid.PartID("0"),
			Type:  event.EventMessage,
			Extra: eventRaw,
		}},
	}
	if evtID, msgID, err := cc.sendViaPortal(ctx, portal, converted, ""); err == nil && evtID != "" {
		cc.approvals.SetData(approvalID, func(data any) any {
			if d, ok := data.(*pendingToolApprovalDataCodex); ok {
				d.ApprovalEventID = evtID
				d.ApprovalNetworkMsgID = msgID
			}
			return data
		})
	}
}

// HandleMatrixActionResponse handles com.beeper.action_response events (MSC1485 action hints).
// This implements bridgev2.ActionResponseHandlingNetworkAPI.
func (cc *CodexClient) HandleMatrixActionResponse(ctx context.Context, msg *bridgev2.MatrixActionResponse) error {
	if msg == nil || msg.Content == nil || msg.Portal == nil {
		return nil
	}
	log := cc.loggerForContext(ctx)

	parsed := bridgeadapter.ParseActionResponse(msg.Content)
	if parsed == nil {
		log.Warn().Msg("action response: failed to parse payload")
		return nil
	}

	approve, _, ok := bridgeadapter.ActionDecisionFromString(parsed.ActionID)
	if !ok {
		log.Warn().Str("action_id", parsed.ActionID).Msg("action response: unknown action_id")
		return nil
	}

	approvalID := strings.TrimSpace(parsed.ApprovalID)
	if approvalID == "" {
		log.Warn().Msg("action response: missing approval_id in context")
		return nil
	}

	err := cc.resolveToolApproval(approvalID, ToolApprovalDecisionCodex{
		Approve:   approve,
		DecidedAt: time.Now(),
		DecidedBy: msg.Event.Sender,
	})
	if err != nil {
		log.Warn().Err(err).Str("approval_id", approvalID).Msg("action response: failed to resolve approval")
		cc.sendToast(ctx, msg.Portal, bridgeadapter.ApprovalErrorToastText(err), aiToastTypeError)
	}
	return nil
}

func (cc *CodexClient) emitUIFinish(ctx context.Context, portal *bridgev2.Portal, state *streamingState, model string, finishReason string) {
	cc.uiEmitter(state).EmitUIFinish(ctx, portal, finishReason, cc.buildUIMessageMetadata(state, model, true, finishReason))
	if state != nil && state.session != nil {
		state.session.End(ctx, streamtransport.EndReason(finishReason))
		state.session = nil
	}
}

func (cc *CodexClient) buildCanonicalUIMessage(state *streamingState, model string, finishReason string) map[string]any {
	parts := msgconv.ContentParts(
		strings.TrimSpace(state.accumulated.String()),
		strings.TrimSpace(state.reasoning.String()),
	)
	if toolParts := msgconv.ToolCallParts(state.toolCalls, string(matrixevents.ToolTypeProvider), string(matrixevents.ResultStatusSuccess), string(matrixevents.ResultStatusDenied)); len(toolParts) > 0 {
		parts = append(parts, toolParts...)
	}
	return msgconv.BuildUIMessage(msgconv.UIMessageParams{
		TurnID:     state.turnID,
		Role:       "assistant",
		Metadata:   cc.buildUIMessageMetadata(state, model, true, finishReason),
		Parts:      parts,
		SourceURLs: citations.BuildSourceParts(state.sourceCitations, state.sourceDocuments),
		FileParts:  citations.GeneratedFilesToParts(state.generatedFiles),
	})
}

func (cc *CodexClient) sendFinalAssistantTurn(ctx context.Context, portal *bridgev2.Portal, state *streamingState, model string, finishReason string) {
	if portal == nil || portal.MXID == "" || state == nil || state.initialEventID == "" {
		return
	}
	if state.suppressSend {
		return
	}
	rendered := format.RenderMarkdown(state.accumulated.String(), true, true)

	// Safety-split oversized responses into multiple Matrix events
	var continuationBody string
	if len(rendered.Body) > streamtransport.MaxMatrixEventBodyBytes {
		firstBody, rest := streamtransport.SplitAtMarkdownBoundary(rendered.Body, streamtransport.MaxMatrixEventBodyBytes)
		continuationBody = rest
		rendered = format.RenderMarkdown(firstBody, true, true)
	}

	uiMessage := cc.buildCanonicalUIMessage(state, model, finishReason)
	topLevelExtra := map[string]any{
		matrixevents.BeeperAIKey:        uiMessage,
		"com.beeper.dont_render_edited": true,
		"m.mentions":                    map[string]any{},
	}

	sender := cc.senderForPortal()
	cc.UserLogin.QueueRemoteEvent(&CodexRemoteEdit{
		portal:        portal.PortalKey,
		sender:        sender,
		targetMessage: state.networkMessageID,
		timestamp:     time.Now(),
		preBuilt: &bridgev2.ConvertedEdit{
			ModifiedParts: []*bridgev2.ConvertedEditPart{{
				Type: event.EventMessage,
				Content: &event.MessageEventContent{
					MsgType:       event.MsgText,
					Body:          rendered.Body,
					Format:        rendered.Format,
					FormattedBody: rendered.FormattedBody,
				},
				Extra:         map[string]any{"m.mentions": map[string]any{}},
				TopLevelExtra: topLevelExtra,
			}},
		},
	})
	cc.loggerForContext(ctx).Debug().
		Str("initial_event_id", state.initialEventID.String()).
		Str("turn_id", state.turnID).
		Bool("has_thinking", state.reasoning.Len() > 0).
		Int("tool_calls", len(state.toolCalls)).
		Msg("Queued final assistant turn edit")

	// Send continuation messages for overflow
	for continuationBody != "" {
		var chunk string
		chunk, continuationBody = streamtransport.SplitAtMarkdownBoundary(continuationBody, streamtransport.MaxMatrixEventBodyBytes)
		cc.sendContinuationMessage(ctx, portal, chunk)
	}
}

// sendContinuationMessage sends overflow text as a new (non-edit) message from the bot.
func (cc *CodexClient) sendContinuationMessage(ctx context.Context, portal *bridgev2.Portal, body string) {
	if portal == nil || portal.MXID == "" {
		return
	}
	rendered := format.RenderMarkdown(body, true, true)
	raw := map[string]any{
		"msgtype":                 event.MsgText,
		"body":                    rendered.Body,
		"format":                  rendered.Format,
		"formatted_body":          rendered.FormattedBody,
		"com.beeper.continuation": true,
		"m.mentions":              map[string]any{},
	}
	sender := cc.senderForPortal()
	cc.UserLogin.QueueRemoteEvent(&CodexRemoteMessage{
		portal:    portal.PortalKey,
		id:        newMessageID(),
		sender:    sender,
		timestamp: time.Now(),
		preBuilt: &bridgev2.ConvertedMessage{
			Parts: []*bridgev2.ConvertedMessagePart{{
				ID:      networkid.PartID("0"),
				Type:    event.EventMessage,
				Content: &event.MessageEventContent{MsgType: event.MsgText, Body: body},
				Extra:   raw,
			}},
		},
	})
	cc.loggerForContext(ctx).Debug().Int("body_len", len(body)).Msg("Queued continuation message for oversized response")
}

func (cc *CodexClient) saveAssistantMessage(ctx context.Context, portal *bridgev2.Portal, state *streamingState, model string, finishReason string) {
	if portal == nil || state == nil || state.initialEventID == "" {
		return
	}
	log := cc.loggerForContext(ctx)

	// Collect generated file references for multimodal history re-injection.
	var genFiles []GeneratedFileRef
	if len(state.generatedFiles) > 0 {
		genFiles = make([]GeneratedFileRef, 0, len(state.generatedFiles))
		for _, f := range state.generatedFiles {
			genFiles = append(genFiles, GeneratedFileRef{URL: f.URL, MimeType: f.MediaType})
		}
	}

	fullMeta := &MessageMetadata{
		Role:               "assistant",
		Body:               state.accumulated.String(),
		FinishReason:       finishReason,
		Model:              model,
		TurnID:             state.turnID,
		AgentID:            state.agentID,
		ToolCalls:          state.toolCalls,
		StartedAtMs:        state.startedAtMs,
		FirstTokenAtMs:     state.firstTokenAtMs,
		CompletedAtMs:      state.completedAtMs,
		HasToolCalls:       len(state.toolCalls) > 0,
		CanonicalSchema:    "ai-sdk-ui-message-v1",
		CanonicalUIMessage: cc.buildCanonicalUIMessage(state, model, finishReason),
		GeneratedFiles:     genFiles,
		ThinkingContent:    state.reasoning.String(),
		ThinkingTokenCount: len(strings.Fields(state.reasoning.String())),
		PromptTokens:       state.promptTokens,
		CompletionTokens:   state.completionTokens,
		ReasoningTokens:    state.reasoningTokens,
	}

	// If the message was sent via sendViaPortal, the DB row already exists — update it.
	if state.networkMessageID != "" {
		existing, err := cc.UserLogin.Bridge.DB.Message.GetPartByMXID(ctx, state.initialEventID)
		if err == nil && existing != nil {
			existing.Metadata = fullMeta
			if err := cc.UserLogin.Bridge.DB.Message.Update(ctx, existing); err != nil {
				log.Warn().Err(err).Str("msg_id", string(existing.ID)).Msg("Failed to update assistant message metadata")
			} else {
				log.Debug().Str("msg_id", string(existing.ID)).Msg("Updated assistant message metadata")
			}
			return
		}
		log.Warn().Err(err).Stringer("mxid", state.initialEventID).Msg("Could not find existing DB row for update, falling back to insert")
	}

	assistantMsg := &database.Message{
		ID:        bridgeadapter.MatrixMessageID(state.initialEventID),
		Room:      portal.PortalKey,
		SenderID:  codexGhostID,
		MXID:      state.initialEventID,
		Timestamp: time.Now(),
		Metadata:  fullMeta,
	}
	if err := cc.UserLogin.Bridge.DB.Message.Insert(ctx, assistantMsg); err != nil {
		log.Warn().Err(err).Msg("Failed to save assistant message")
	} else {
		log.Debug().Str("msg_id", string(assistantMsg.ID)).Msg("Saved assistant message to database")
	}
}

// --- Approvals ---

type ToolApprovalDecisionCodex struct {
	Approve   bool
	Reason    string
	DecidedAt time.Time
	DecidedBy id.UserID
}

// pendingToolApprovalDataCodex holds codex-specific metadata stored in
// ApprovalManager's PendingApproval.Data field.
type pendingToolApprovalDataCodex struct {
	ApprovalID           string
	ToolCallID           string
	ToolName             string
	ApprovalEventID      id.EventID
	ApprovalNetworkMsgID networkid.MessageID
}

func (cc *CodexClient) registerToolApproval(approvalID, toolCallID, toolName string, ttl time.Duration) (*bridgeadapter.PendingApproval[ToolApprovalDecisionCodex], bool) {
	data := &pendingToolApprovalDataCodex{
		ApprovalID: strings.TrimSpace(approvalID),
		ToolCallID: strings.TrimSpace(toolCallID),
		ToolName:   strings.TrimSpace(toolName),
	}
	return cc.approvals.Register(approvalID, ttl, data)
}

func (cc *CodexClient) resolveToolApproval(approvalID string, decision ToolApprovalDecisionCodex) error {
	return cc.approvals.Resolve(approvalID, decision)
}

func (cc *CodexClient) waitToolApproval(ctx context.Context, approvalID string) (ToolApprovalDecisionCodex, bool) {
	return cc.approvals.Wait(ctx, approvalID)
}

func (cc *CodexClient) handleApprovalRequest(
	ctx context.Context, req codexrpc.Request,
	defaultToolName string, extractInput func(json.RawMessage) map[string]any,
) (any, *codexrpc.RPCError) {
	approvalID := strings.Trim(string(req.ID), "\"")
	var params struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		ItemID   string `json:"itemId"`
	}
	_ = json.Unmarshal(req.Params, &params)

	cc.activeMu.Lock()
	active := cc.activeTurns[codexTurnKey(params.ThreadID, params.TurnID)]
	cc.activeMu.Unlock()
	if active == nil || params.ThreadID != active.threadID || params.TurnID != active.turnID {
		return map[string]any{"decision": "decline"}, nil
	}

	toolCallID := strings.TrimSpace(params.ItemID)
	if toolCallID == "" {
		toolCallID = defaultToolName
	}
	toolName := defaultToolName
	ttlSeconds := 600

	cc.setApprovalStateTracking(active.state, approvalID, toolCallID, toolName)

	inputMap := extractInput(req.Params)
	cc.ensureUIToolInputStart(ctx, active.portal, active.state, toolCallID, toolName, true, inputMap)
	approvalTTL := time.Duration(ttlSeconds) * time.Second
	cc.registerToolApproval(approvalID, toolCallID, toolName, approvalTTL)

	cc.emitUIToolApprovalRequest(ctx, active.portal, active.state, approvalID, toolCallID, toolName, ttlSeconds)
	approvalExpiresAtMs := time.Now().Add(approvalTTL).UnixMilli()
	cc.sendToolCallApprovalEvent(ctx, active.portal, active.state, toolCallID, toolName, approvalID, approvalExpiresAtMs)
	cc.sendActionHintsApprovalEvent(ctx, active.portal, active.state, toolCallID, toolName, approvalID, approvalExpiresAtMs)
	cc.sendSystemNoticeOnce(ctx, active.portal, active.state, "codex-approval:"+approvalID, fmt.Sprintf("Approval required (%s): !ai approve %s <allow|deny> [reason]", toolName, approvalID))

	if active.meta != nil {
		if lvl, _ := stringutil.NormalizeElevatedLevel(active.meta.ElevatedLevel); lvl == "full" {
			return map[string]any{"decision": "accept"}, nil
		}
	}

	decision, ok := cc.waitToolApproval(ctx, approvalID)
	if !ok {
		return map[string]any{"decision": "decline"}, nil
	}
	if decision.Approve {
		return map[string]any{"decision": "accept"}, nil
	}
	return map[string]any{"decision": "decline"}, nil
}

func (cc *CodexClient) handleCommandApprovalRequest(ctx context.Context, req codexrpc.Request) (any, *codexrpc.RPCError) {
	return cc.handleApprovalRequest(ctx, req, "commandExecution", func(raw json.RawMessage) map[string]any {
		var p struct {
			Command *string `json:"command"`
			Cwd     *string `json:"cwd"`
			Reason  *string `json:"reason"`
		}
		_ = json.Unmarshal(raw, &p)
		return map[string]any{"command": p.Command, "cwd": p.Cwd, "reason": p.Reason}
	})
}

func (cc *CodexClient) handleFileChangeApprovalRequest(ctx context.Context, req codexrpc.Request) (any, *codexrpc.RPCError) {
	return cc.handleApprovalRequest(ctx, req, "fileChange", func(raw json.RawMessage) map[string]any {
		var p struct {
			Reason    *string `json:"reason"`
			GrantRoot *string `json:"grantRoot"`
		}
		_ = json.Unmarshal(raw, &p)
		return map[string]any{"reason": p.Reason, "grantRoot": p.GrantRoot}
	})
}

func (cc *CodexClient) sendSystemNoticeOnce(ctx context.Context, portal *bridgev2.Portal, state *streamingState, key string, message string) {
	key = strings.TrimSpace(key)
	if key == "" || state == nil {
		cc.sendSystemNotice(ctx, portal, message)
		return
	}
	if state.codexTimelineNotices == nil {
		state.codexTimelineNotices = make(map[string]bool)
	}
	if state.codexTimelineNotices[key] {
		return
	}
	state.codexTimelineNotices[key] = true
	cc.sendSystemNotice(ctx, portal, message)
}

// setApprovalStateTracking populates the streaming state maps used for approval correlation.
func (cc *CodexClient) setApprovalStateTracking(state *streamingState, approvalID, toolCallID, toolName string) {
	if state == nil {
		return
	}
	state.ui.InitMaps()
	state.ui.UIToolCallIDByApproval[approvalID] = toolCallID
	state.ui.UIToolApprovalRequested[approvalID] = true
	state.ui.UIToolNameByToolCallID[toolCallID] = toolName
	state.ui.UIToolTypeByToolCallID[toolCallID] = matrixevents.ToolTypeProvider
}
