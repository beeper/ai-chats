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
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/bridges/ai/msgconv"
	"github.com/beeper/agentremote/bridges/codex/codexrpc"
	"github.com/beeper/agentremote/pkg/matrixevents"
	"github.com/beeper/agentremote/pkg/shared/citations"
	"github.com/beeper/agentremote/pkg/shared/streamui"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	bridgesdk "github.com/beeper/agentremote/sdk"
	"github.com/beeper/agentremote/turns"
)

var (
	_ bridgev2.NetworkAPI                    = (*CodexClient)(nil)
	_ bridgev2.BackfillingNetworkAPI         = (*CodexClient)(nil)
	_ bridgev2.DeleteChatHandlingNetworkAPI  = (*CodexClient)(nil)
	_ bridgev2.IdentifierResolvingNetworkAPI = (*CodexClient)(nil)
	_ bridgev2.ContactListingNetworkAPI      = (*CodexClient)(nil)
	_ bridgev2.ReactionHandlingNetworkAPI    = (*CodexClient)(nil)
)

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

type codexPendingQueue []*codexPendingMessage

type CodexClient struct {
	agentremote.ClientBase
	UserLogin *bridgev2.UserLogin
	connector *CodexConnector
	log       zerolog.Logger

	defaultChatMu sync.Mutex // serializes default-room bootstrap and welcome notices
	rpcMu         sync.Mutex
	rpc           *codexrpc.Client

	notifCh   chan codexNotif
	notifDone chan struct{} // closed on Disconnect to stop dispatchNotifications

	loggedIn atomic.Bool

	// streamEventHook, when set, receives the stream event envelope (including "part")
	// instead of sending ephemeral Matrix events. Used by tests.
	streamEventHook func(turnID string, seq int, content map[string]any, txnID string)

	activeMu    sync.Mutex
	activeTurns map[string]*codexActiveTurn // turnKey(threadId, turnId) -> active turn (for approvals)

	subMu            sync.Mutex
	turnSubs         map[string]chan codexNotif // turnKey(threadId, turnId) -> notification channel
	startDispatching func()                     // starts dispatchNotifications goroutine exactly once

	loadedMu      sync.Mutex
	loadedThreads map[string]bool // threadId -> loaded via thread/start|thread/resume

	approvalFlow *agentremote.ApprovalFlow[*pendingToolApprovalDataCodex]

	scheduleBootstrapOnce func() // starts bootstrap goroutine exactly once

	roomMu          sync.Mutex
	activeRooms     map[id.RoomID]bool
	pendingMessages map[id.RoomID]codexPendingQueue

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
	log := login.Log.With().Str("component", "codex").Logger()
	cc := &CodexClient{
		UserLogin:       login,
		connector:       connector,
		log:             log,
		notifCh:         make(chan codexNotif, 4096),
		notifDone:       make(chan struct{}),
		loadedThreads:   make(map[string]bool),
		activeTurns:     make(map[string]*codexActiveTurn),
		turnSubs:        make(map[string]chan codexNotif),
		activeRooms:     make(map[id.RoomID]bool),
		pendingMessages: make(map[id.RoomID]codexPendingQueue),
	}
	cc.InitClientBase(login, cc)
	cc.approvalFlow = agentremote.NewApprovalFlow(agentremote.ApprovalFlowConfig[*pendingToolApprovalDataCodex]{
		Login:             func() *bridgev2.UserLogin { return cc.UserLogin },
		Sender:            func(_ *bridgev2.Portal) bridgev2.EventSender { return cc.senderForPortal() },
		BackgroundContext: cc.backgroundContext,
		IDPrefix:          "codex",
		LogKey:            "codex_msg_id",
		RoomIDFromData: func(data *pendingToolApprovalDataCodex) id.RoomID {
			if data == nil {
				return ""
			}
			return data.RoomID
		},
		SendNotice: func(ctx context.Context, portal *bridgev2.Portal, msg string) {
			cc.sendSystemNotice(ctx, portal, msg)
		},
	})
	cc.startDispatching = sync.OnceFunc(func() {
		go cc.dispatchNotifications()
	})
	cc.scheduleBootstrapOnce = sync.OnceFunc(func() {
		go cc.bootstrap(cc.UserLogin.Bridge.BackgroundCtx)
	})
	return cc, nil
}

func (cc *CodexClient) SetUserLogin(login *bridgev2.UserLogin) {
	cc.UserLogin = login
	cc.ClientBase.SetUserLogin(login)
}

func (cc *CodexClient) loggerForContext(ctx context.Context) *zerolog.Logger {
	return agentremote.LoggerFromContext(ctx, &cc.log)
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
	cc.pendingMessages = make(map[id.RoomID]codexPendingQueue)
	cc.roomMu.Unlock()
}

func (cc *CodexClient) IsLoggedIn() bool {
	return cc.loggedIn.Load()
}

func (cc *CodexClient) GetUserLogin() *bridgev2.UserLogin { return cc.UserLogin }

func (cc *CodexClient) GetApprovalHandler() agentremote.ApprovalReactionHandler {
	return cc.approvalFlow
}

func (cc *CodexClient) LogoutRemote(ctx context.Context) {
	meta := loginMetadata(cc.UserLogin)
	// Only managed per-login auth should trigger upstream account/logout.
	if !isHostAuthLogin(meta) {
		if err := cc.ensureRPC(cc.backgroundContext(ctx)); err == nil && cc.rpc != nil {
			callCtx, cancel := context.WithTimeout(cc.backgroundContext(ctx), 10*time.Second)
			defer cancel()
			var out map[string]any
			_ = cc.rpc.Call(callCtx, "account/logout", nil, &out)
		}
	}
	// Best-effort: remove on-disk Codex state for this login.
	cc.purgeCodexHomeBestEffort(ctx)
	// Best-effort: remove on-disk per-room Codex working dirs.
	cc.purgeCodexCwdsBestEffort(ctx)

	cc.Disconnect()

	if cc.connector != nil {
		agentremote.RemoveClientFromCache(&cc.connector.clientsMu, cc.connector.clients, cc.UserLogin.ID)
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
	if !isManagedAuthLogin(meta) {
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
	metaTitle := ""
	if meta != nil {
		metaTitle = meta.Title
	}
	if meta == nil || !meta.IsCodexRoom {
		return agentremote.BuildChatInfoWithFallback(metaTitle, portal.Name, "Codex", portal.Topic), nil
	}
	title := codexPortalTitle(portal)
	return cc.composeCodexChatInfo(title, strings.TrimSpace(meta.CodexThreadID) != ""), nil
}

func (cc *CodexClient) GetUserInfo(_ context.Context, _ *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	return codexSDKAgent().UserInfo(), nil
}

func (cc *CodexClient) ResolveIdentifier(ctx context.Context, identifier string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	if cc == nil || cc.UserLogin == nil || cc.UserLogin.Bridge == nil {
		return nil, errors.New("login unavailable")
	}
	if !isCodexIdentifier(identifier) {
		return nil, fmt.Errorf("unknown identifier: %s", identifier)
	}

	ghost, err := cc.UserLogin.Bridge.GetGhostByID(ctx, codexGhostID)
	if err != nil {
		return nil, fmt.Errorf("failed to get Codex ghost: %w", err)
	}

	var chat *bridgev2.CreateChatResponse
	if createChat {
		if err := cc.ensureDefaultCodexChat(ctx); err != nil {
			return nil, fmt.Errorf("failed to ensure Codex chat: %w", err)
		}
		portal, err := cc.UserLogin.Bridge.GetPortalByKey(ctx, defaultCodexChatPortalKey(cc.UserLogin.ID))
		if err != nil {
			return nil, fmt.Errorf("failed to load Codex chat: %w", err)
		}
		if portal == nil {
			return nil, errors.New("codex chat unavailable")
		}
		meta := portalMeta(portal)
		chatInfo := cc.composeCodexChatInfo(codexPortalTitle(portal), strings.TrimSpace(meta.CodexThreadID) != "")
		chat = &bridgev2.CreateChatResponse{
			PortalKey:  portal.PortalKey,
			PortalInfo: chatInfo,
			Portal:     portal,
		}
	}

	return &bridgev2.ResolveIdentifierResponse{
		UserID:   codexGhostID,
		UserInfo: codexSDKAgent().UserInfo(),
		Ghost:    ghost,
		Chat:     chat,
	}, nil
}

func (cc *CodexClient) GetContactList(ctx context.Context) ([]*bridgev2.ResolveIdentifierResponse, error) {
	resp, err := cc.ResolveIdentifier(ctx, "codex", false)
	if err != nil {
		return nil, err
	}
	return []*bridgev2.ResolveIdentifierResponse{resp}, nil
}

func codexPortalTitle(portal *bridgev2.Portal) string {
	if portal == nil {
		return "Codex"
	}
	if meta := portalMeta(portal); meta != nil {
		if title := strings.TrimSpace(meta.Title); title != "" {
			return title
		}
	}
	if name := strings.TrimSpace(portal.Name); name != "" {
		return name
	}
	return "Codex"
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
		return nil, agentremote.UnsupportedMessageStatus(errors.New("not a Codex room"))
	}
	if agentremote.IsMatrixBotUser(ctx, cc.UserLogin.Bridge, msg.Event.Sender) {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	// Only text messages.
	switch msg.Content.MsgType {
	case event.MsgText, event.MsgNotice, event.MsgEmote:
	default:
		return nil, agentremote.UnsupportedMessageStatus(fmt.Errorf("%s messages are not supported", msg.Content.MsgType))
	}
	if msg.Content.RelatesTo != nil && msg.Content.RelatesTo.GetReplaceID() != "" {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	body := strings.TrimSpace(msg.Content.Body)
	if body == "" {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	if meta.AwaitingCwdSetup {
		path, err := resolveCodexWorkingDirectory(strings.TrimSpace(msg.Content.Body))
		if err != nil {
			cc.sendSystemNotice(ctx, portal, "That path must be absolute. `~/...` is also accepted.")
			return &bridgev2.MatrixMessageResponse{Pending: false}, nil
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			cc.sendSystemNotice(ctx, portal, fmt.Sprintf("That path doesn't exist or isn't a directory: %s", path))
			return &bridgev2.MatrixMessageResponse{Pending: false}, nil
		}
		meta.CodexCwd = path
		meta.AwaitingCwdSetup = false
		if err := portal.Save(ctx); err != nil {
			return nil, messageSendStatusError(err, "Failed to save portal.", "")
		}
		if err := cc.ensureRPC(cc.backgroundContext(ctx)); err != nil {
			return nil, messageSendStatusError(err, "Codex isn't available. Sign in again.", "")
		}
		if err := cc.ensureCodexThread(ctx, portal, meta); err != nil {
			return nil, messageSendStatusError(err, "Failed to start Codex thread.", "")
		}
		cc.sendSystemNotice(ctx, portal, fmt.Sprintf("Working directory set to %s", path))
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
		ID:        agentremote.MatrixMessageID(msg.Event.ID),
		MXID:      msg.Event.ID,
		Room:      portal.PortalKey,
		SenderID:  humanUserID(cc.UserLogin.ID),
		Timestamp: agentremote.MatrixEventTimestamp(msg.Event),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: agentremote.BaseMessageMetadata{Role: "user", Body: body},
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

	if !cc.acquireRoomIfQueueEmpty(roomID) {
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
	conv := bridgesdk.NewConversation(ctx, cc.UserLogin, portal, cc.senderForPortal(), cc.connector.sdkConfig, cc)
	source := bridgesdk.UserMessageSource(sourceEvent.ID.String())
	turn := conv.StartTurn(ctx, codexSDKAgent(), source)
	turn.SetStreamHook(func(turnID string, seq int, content map[string]any, txnID string) bool {
		if cc.streamEventHook == nil {
			return false
		}
		cc.streamEventHook(turnID, seq, content, txnID)
		return true
	})
	turn.SetApprovalRequester(func(callCtx context.Context, sdkTurn *bridgesdk.Turn, req bridgesdk.ApprovalRequest) bridgesdk.ApprovalHandle {
		return cc.requestSDKApproval(callCtx, portal, state, sdkTurn, req)
	})
	turn.SetFinalMetadataBuilder(func(sdkTurn *bridgesdk.Turn, finishReason string) any {
		return cc.buildSDKFinalMetadata(sdkTurn, state, model, finishReason)
	})
	state.turn = turn
	state.turnID = turn.ID()
	state.agentID = string(codexGhostID)
	state.initialEventID = sourceEvent.ID
	turn.SetMetadata(cc.buildUIMessageMetadata(state, model, false, ""))
	turn.StepStart()

	approvalPolicy := "untrusted"
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
		turn.EndWithError(err.Error())
		return
	}
	turnID := strings.TrimSpace(turnStart.Turn.ID)
	if turnID == "" {
		turnID = "turn_unknown"
	}
	cc.markMessageSendSuccess(ctx, portal, sourceEvent, state)

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
		turn.SetMetadata(cc.buildUIMessageMetadata(state, model, true, finishStatus))
		turn.EndWithError(completedErr)
		return
	}
	turn.SetMetadata(cc.buildUIMessageMetadata(state, model, true, finishStatus))
	turn.End(finishStatus)
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
	// Each ID field, when present, must match the expected value.
	for _, pair := range [][2]string{
		{strings.TrimSpace(p.ThreadID), threadID},
		{strings.TrimSpace(p.TurnID), turnID},
		{strings.TrimSpace(p.Turn.ID), turnID},
	} {
		if pair[0] != "" && pair[0] != pair[1] {
			return "", "", false
		}
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

	// Streaming for these types comes via dedicated delta events.
	if probe.Type == "agentMessage" || probe.Type == "reasoning" {
		return
	}

	// All remaining item types share the same unmarshal + ensureUIToolInputStart pattern.
	var it map[string]any
	_ = json.Unmarshal(raw, &it)

	toolName := probe.Type
	switch probe.Type {
	case "mcpToolCall":
		if name, _ := it["tool"].(string); strings.TrimSpace(name) != "" {
			toolName = name
		}
	case "enteredReviewMode", "exitedReviewMode":
		toolName = "review"
	}

	cc.ensureUIToolInputStart(ctx, portal, state, itemID, toolName, true, it)

	// Type-specific side effects (system notices).
	switch probe.Type {
	case "webSearch":
		notice := "Codex started web search."
		if q, _ := it["query"].(string); strings.TrimSpace(q) != "" {
			notice = fmt.Sprintf("Codex started web search: %s", strings.TrimSpace(q))
		}
		cc.sendSystemNoticeOnce(ctx, portal, state, "websearch:"+itemID, notice)
	case "imageView":
		cc.sendSystemNoticeOnce(ctx, portal, state, "imageview:"+itemID, "Codex viewed an image.")
	case "enteredReviewMode":
		cc.sendSystemNoticeOnce(ctx, portal, state, "review:entered:"+itemID, "Codex entered review mode.")
	case "exitedReviewMode":
		cc.sendSystemNoticeOnce(ctx, portal, state, "review:exited:"+itemID, "Codex exited review mode.")
	case "contextCompaction":
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

func emitNewArtifacts(ctx context.Context, portal *bridgev2.Portal, emitter *streamui.Emitter, docs []citations.SourceDocument, files []citations.GeneratedFilePart) {
	if emitter == nil {
		return
	}
	for _, document := range docs {
		emitter.EmitUISourceDocument(ctx, portal, document)
	}
	for _, file := range files {
		emitter.EmitUIFile(ctx, portal, file.URL, file.MediaType)
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
			cc.uiEmitter(state).EmitUIToolOutputDenied(ctx, portal, itemID)
		case "failed":
			errText := "tool failed"
			if errObj, ok := it["error"].(map[string]any); ok {
				if msg, ok := errObj["message"].(string); ok && strings.TrimSpace(msg) != "" {
					errText = strings.TrimSpace(msg)
				}
			}
			cc.uiEmitter(state).EmitUIToolOutputError(ctx, portal, itemID, errText, true)
		default:
			cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, it, true, false)
		}
		newDocs, newFiles := collectToolOutputArtifacts(state, it)
		emitNewArtifacts(ctx, portal, cc.uiEmitter(state), newDocs, newFiles)

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
		cc.emitProviderJSONToolOutput(ctx, portal, state, itemID, "collabToolCall", raw, providerJSONToolOutputOptions{collectArtifacts: true})
	case "webSearch":
		cc.emitProviderJSONToolOutput(ctx, portal, state, itemID, "webSearch", raw, providerJSONToolOutputOptions{
			collectArtifacts:        true,
			collectCitations:        true,
			appendBeforeSideEffects: true,
		})
	case "imageView":
		cc.emitProviderJSONToolOutput(ctx, portal, state, itemID, "imageView", raw, providerJSONToolOutputOptions{collectArtifacts: true})
	case "plan":
		var it struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &it)
		if !cc.emitTrimmedProviderToolTextOutput(ctx, portal, state, itemID, "plan", "text", it.Text) {
			return
		}
	case "enteredReviewMode":
		cc.emitProviderJSONToolOutput(ctx, portal, state, itemID, "review", raw, providerJSONToolOutputOptions{})
	case "exitedReviewMode":
		var it struct {
			Review string `json:"review"`
		}
		_ = json.Unmarshal(raw, &it)
		if !cc.emitTrimmedProviderToolTextOutput(ctx, portal, state, itemID, "review", "review", it.Review) {
			return
		}
	case "contextCompaction":
		cc.emitProviderJSONToolOutput(ctx, portal, state, itemID, "contextCompaction", raw, providerJSONToolOutputOptions{})
		cc.sendSystemNoticeOnce(ctx, portal, state, "compaction:completed:"+itemID, "Codex finished compacting context.")
	}
}

type providerJSONToolOutputOptions struct {
	collectArtifacts        bool
	collectCitations        bool
	appendBeforeSideEffects bool
}

func (cc *CodexClient) emitProviderJSONToolOutput(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	itemID string,
	toolName string,
	raw []byte,
	opts providerJSONToolOutputOptions,
) {
	var it map[string]any
	_ = json.Unmarshal(raw, &it)
	cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, it, true, false)
	appendToolCall := func() {
		state.toolCalls = append(state.toolCalls, newProviderToolCall(itemID, toolName, it))
	}
	if opts.appendBeforeSideEffects {
		appendToolCall()
	}
	if opts.collectCitations {
		if outputJSON, err := json.Marshal(it); err == nil {
			collectToolOutputCitations(state, toolName, string(outputJSON))
			for _, citation := range state.sourceCitations {
				cc.uiEmitter(state).EmitUISourceURL(ctx, portal, citation)
			}
		}
	}
	if opts.collectArtifacts {
		newDocs, newFiles := collectToolOutputArtifacts(state, it)
		emitNewArtifacts(ctx, portal, cc.uiEmitter(state), newDocs, newFiles)
	}
	if !opts.appendBeforeSideEffects {
		appendToolCall()
	}
}

func (cc *CodexClient) emitTrimmedProviderToolTextOutput(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	itemID string,
	toolName string,
	field string,
	value string,
) bool {
	text := strings.TrimSpace(value)
	if text == "" {
		return false
	}
	cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, text, true, false)
	state.toolCalls = append(state.toolCalls, newProviderToolCall(itemID, toolName, map[string]any{field: text}))
	return true
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
	var env []string
	if codexHome != "" {
		if err := os.MkdirAll(codexHome, 0o700); err != nil {
			return err
		}
		env = []string{"CODEX_HOME=" + codexHome}
	}
	launch, err := cc.connector.resolveAppServerLaunch()
	if err != nil {
		return err
	}
	rpc, err := codexrpc.StartProcess(ctx, codexrpc.ProcessConfig{
		Command:      cmd,
		Args:         launch.Args,
		Env:          env,
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

	cc.startDispatching()

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
		Turn     *struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "", "", false
	}
	threadID = strings.TrimSpace(p.ThreadID)
	turnID = strings.TrimSpace(p.TurnID)
	if turnID == "" && p.Turn != nil {
		turnID = strings.TrimSpace(p.Turn.ID)
	}
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

func (cc *CodexClient) scheduleBootstrap() {
	cc.scheduleBootstrapOnce()
}

func (cc *CodexClient) bootstrap(ctx context.Context) {
	cc.waitForLoginPersisted(ctx)
	syncSucceeded := true
	if err := cc.ensureDefaultCodexChat(cc.backgroundContext(ctx)); err != nil {
		cc.log.Warn().Err(err).Msg("Failed to ensure default Codex chat during bootstrap")
		syncSucceeded = false
	}
	if err := cc.syncStoredCodexThreads(cc.backgroundContext(ctx)); err != nil {
		cc.log.Warn().Err(err).Msg("Failed to sync Codex threads during bootstrap")
		syncSucceeded = false
	}
	meta := loginMetadata(cc.UserLogin)
	meta.ChatsSynced = syncSucceeded
	_ = cc.UserLogin.Save(ctx)
}

func (cc *CodexClient) waitForLoginPersisted(ctx context.Context) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(60 * time.Second)
	for {
		_, err := cc.UserLogin.Bridge.DB.UserLogin.GetByID(ctx, cc.UserLogin.ID)
		if err == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-timeout:
			cc.log.Warn().Msg("Timed out waiting for login to persist, continuing anyway")
			return
		case <-ticker.C:
		}
	}
}

func (cc *CodexClient) ensureDefaultCodexChat(ctx context.Context) error {
	cc.defaultChatMu.Lock()
	defer cc.defaultChatMu.Unlock()

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
		info := cc.composeCodexChatInfo(meta.Title, false)
		if err := portal.CreateMatrixRoom(ctx, cc.UserLogin, info); err != nil {
			return err
		}
		agentremote.SendAIRoomInfo(ctx, portal, agentremote.AIRoomKindAgent)
		cc.sendSystemNotice(ctx, portal, "AI Chats can make mistakes.")
		cc.sendSystemNotice(ctx, portal, "What directory should Codex work in? Send an absolute path or `~/...`.")
		meta.AwaitingCwdSetup = true
		if err := portal.Save(ctx); err != nil {
			return err
		}
		return nil
	}

	// Ensure thread started if directory is already set.
	if strings.TrimSpace(meta.CodexCwd) != "" {
		return cc.ensureCodexThread(ctx, portal, meta)
	}
	return nil
}

func (cc *CodexClient) composeCodexChatInfo(title string, canBackfill bool) *bridgev2.ChatInfo {
	if title == "" {
		title = "Codex"
	}
	return agentremote.BuildDMChatInfo(agentremote.DMChatInfoParams{
		Title:             title,
		HumanUserID:       humanUserID(cc.UserLogin.ID),
		LoginID:           cc.UserLogin.ID,
		BotUserID:         codexGhostID,
		BotDisplayName:    "Codex",
		CanBackfill:       canBackfill,
		CapabilitiesEvent: matrixevents.RoomCapabilitiesEventType,
		SettingsEvent:     matrixevents.RoomSettingsEventType,
	})
}

func resolveCodexWorkingDirectory(raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, rest)
	} else if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = home
	}

	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be absolute")
	}
	return filepath.Clean(path), nil
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
		return errors.New("codex working directory not set")
	}
	if _, err := os.Stat(meta.CodexCwd); err != nil {
		return fmt.Errorf("working directory %s no longer exists", meta.CodexCwd)
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
		"approvalPolicy": "untrusted",
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
		"approvalPolicy": "untrusted",
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
	bg := cc.backgroundContext(ctx)
	sendCtx, cancel := context.WithTimeout(bg, 10*time.Second)
	defer cancel()
	cc.sendViaPortal(sendCtx, portal, agentremote.BuildSystemNotice(strings.TrimSpace(message)), "")
}

func (cc *CodexClient) sendPendingStatus(ctx context.Context, portal *bridgev2.Portal, evt *event.Event, message string) {
	st := bridgev2.MessageStatus{
		Status:    event.MessageStatusPending,
		Message:   message,
		IsCertain: true,
	}
	agentremote.SendMatrixMessageStatus(ctx, portal, evt, st)
}

func (cc *CodexClient) markMessageSendSuccess(ctx context.Context, portal *bridgev2.Portal, evt *event.Event, state *streamingState) {
	if state == nil {
		return
	}
	st := bridgev2.MessageStatus{Status: event.MessageStatusSuccess, IsCertain: true}
	agentremote.SendMatrixMessageStatus(ctx, portal, evt, st)
}

func (cc *CodexClient) acquireRoomIfQueueEmpty(roomID id.RoomID) bool {
	cc.roomMu.Lock()
	defer cc.roomMu.Unlock()
	if cc.activeRooms[roomID] || len(cc.pendingMessages[roomID]) > 0 {
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
	cc.pendingMessages[roomID] = append(cc.pendingMessages[roomID], pm)
}

func (cc *CodexClient) beginPendingCodex(roomID id.RoomID) *codexPendingMessage {
	cc.roomMu.Lock()
	defer cc.roomMu.Unlock()
	if cc.activeRooms[roomID] {
		return nil
	}
	queue := cc.pendingMessages[roomID]
	if len(queue) == 0 {
		delete(cc.pendingMessages, roomID)
		return nil
	}
	cc.activeRooms[roomID] = true
	return queue[0]
}

func (cc *CodexClient) popPendingCodex(roomID id.RoomID) *codexPendingMessage {
	cc.roomMu.Lock()
	defer cc.roomMu.Unlock()
	queue := cc.pendingMessages[roomID]
	if len(queue) == 0 {
		delete(cc.pendingMessages, roomID)
		return nil
	}
	pm := queue[0]
	if len(queue) == 1 {
		delete(cc.pendingMessages, roomID)
	} else {
		cc.pendingMessages[roomID] = queue[1:]
	}
	return pm
}

func (cc *CodexClient) processPendingCodex(roomID id.RoomID) {
	pm := cc.beginPendingCodex(roomID)
	if pm == nil {
		return
	}
	ctx := cc.backgroundContext(context.Background())
	if err := cc.ensureRPC(ctx); err != nil {
		cc.log.Warn().Err(err).Stringer("room", roomID).Msg("Pending codex message: RPC unavailable")
		cc.releaseRoom(roomID)
		return
	}
	meta := portalMeta(pm.portal)
	if meta == nil {
		// Bad portal — discard.
		cc.popPendingCodex(roomID)
		cc.releaseRoom(roomID)
		cc.processPendingCodex(roomID)
		return
	}
	if err := cc.ensureCodexThreadLoaded(ctx, pm.portal, meta); err != nil {
		cc.log.Warn().Err(err).Stringer("room", roomID).Msg("Pending codex message: thread load failed")
		cc.releaseRoom(roomID)
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

	msgID := agentremote.NewMessageID("codex")
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:         networkid.PartID("0"),
			Type:       event.EventMessage,
			Content:    &event.MessageEventContent{MsgType: event.MsgText, Body: content},
			Extra:      eventRaw,
			DBMetadata: &MessageMetadata{BaseMessageMetadata: agentremote.BaseMessageMetadata{Role: "assistant", TurnID: turnID}},
		}},
	}

	eventTS := codexStreamEventTimestamp(state, false)
	streamOrder := codexNextLiveStreamOrder(state, eventTS)
	eventID, _, err := cc.sendViaPortalWithOrdering(portal, converted, msgID, eventTS, streamOrder)
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
	ui.EnsureUIToolInputStart(ctx, portal, toolCallID, toolName, providerExecuted, streamui.ToolDisplayTitle(toolName), nil)
	ui.EmitUIToolInputAvailable(ctx, portal, toolCallID, toolName, input, providerExecuted)
}

func (cc *CodexClient) emitUIToolApprovalRequest(
	ctx context.Context, portal *bridgev2.Portal, state *streamingState,
	approvalID, toolCallID, toolName string, presentation agentremote.ApprovalPromptPresentation, ttlSeconds int,
) {
	cc.uiEmitter(state).EmitUIToolApprovalRequest(ctx, portal, approvalID, toolCallID)
	if state == nil {
		return
	}
	cc.approvalFlow.SendPrompt(ctx, portal, agentremote.SendPromptParams{
		ApprovalPromptMessageParams: agentremote.ApprovalPromptMessageParams{
			ApprovalID:     approvalID,
			ToolCallID:     toolCallID,
			ToolName:       toolName,
			TurnID:         state.turnID,
			Presentation:   presentation,
			ReplyToEventID: state.initialEventID,
			ExpiresAt:      agentremote.ComputeApprovalExpiry(ttlSeconds),
		},
		RoomID:    portal.MXID,
		OwnerMXID: cc.UserLogin.UserMXID,
	})
}

func (cc *CodexClient) emitUIFinish(ctx context.Context, portal *bridgev2.Portal, state *streamingState, model string, finishReason string) {
	cc.uiEmitter(state).EmitUIFinish(ctx, portal, finishReason, cc.buildUIMessageMetadata(state, model, true, finishReason))
	if state != nil && state.session != nil {
		state.session.End(ctx, turns.EndReason(finishReason))
		state.session = nil
	}
}

func (cc *CodexClient) buildCanonicalUIMessage(state *streamingState, model string, finishReason string) map[string]any {
	if uiMessage := streamui.SnapshotCanonicalUIMessage(&state.ui); len(uiMessage) > 0 {
		metadata, _ := uiMessage["metadata"].(map[string]any)
		uiMessage["metadata"] = msgconv.MergeUIMessageMetadata(metadata, cc.buildUIMessageMetadata(state, model, true, finishReason))
		return msgconv.AppendUIMessageArtifacts(
			uiMessage,
			citations.BuildSourceParts(state.sourceCitations, state.sourceDocuments),
			citations.GeneratedFilesToParts(state.generatedFiles),
		)
	}
	return msgconv.BuildUIMessage(msgconv.UIMessageParams{
		TurnID:     state.turnID,
		Role:       "assistant",
		Metadata:   cc.buildUIMessageMetadata(state, model, true, finishReason),
		SourceURLs: citations.BuildSourceParts(state.sourceCitations, state.sourceDocuments),
		FileParts:  citations.GeneratedFilesToParts(state.generatedFiles),
	})
}

func (cc *CodexClient) sendFinalAssistantTurn(ctx context.Context, portal *bridgev2.Portal, state *streamingState, model string, finishReason string) {
	if portal == nil || portal.MXID == "" || state == nil || !state.hasEditTarget() {
		return
	}
	if state.suppressSend {
		return
	}
	rendered := format.RenderMarkdown(state.accumulated.String(), true, true)

	// Safety-split oversized responses into multiple Matrix events
	var continuationBody string
	if len(rendered.Body) > turns.MaxMatrixEventBodyBytes {
		firstBody, rest := turns.SplitAtMarkdownBoundary(rendered.Body, turns.MaxMatrixEventBodyBytes)
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
	editTS := codexStreamEventTimestamp(state, true)
	cc.UserLogin.QueueRemoteEvent(&CodexRemoteEdit{
		Portal:        portal.PortalKey,
		Sender:        sender,
		TargetMessage: state.networkMessageID,
		Timestamp:     editTS,
		StreamOrder:   codexNextLiveStreamOrder(state, editTS),
		LogKey:        "codex_edit_target",
		PreBuilt: turns.BuildRenderedConvertedEdit(turns.RenderedMarkdownContent{
			Body:          rendered.Body,
			Format:        rendered.Format,
			FormattedBody: rendered.FormattedBody,
		}, topLevelExtra),
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
		chunk, continuationBody = turns.SplitAtMarkdownBoundary(continuationBody, turns.MaxMatrixEventBodyBytes)
		cc.sendContinuationMessage(ctx, portal, chunk)
	}
}

// sendContinuationMessage sends overflow text as a new (non-edit) message from the bot.
func (cc *CodexClient) sendContinuationMessage(ctx context.Context, portal *bridgev2.Portal, body string) {
	if portal == nil || portal.MXID == "" {
		return
	}
	msg := agentremote.BuildContinuationMessage(portal.PortalKey, body, cc.senderForPortal(), "codex", "codex_msg_id")
	cc.UserLogin.QueueRemoteEvent(msg)
	cc.loggerForContext(ctx).Debug().Int("body_len", len(body)).Msg("Queued continuation message for oversized response")
}

func (cc *CodexClient) saveAssistantMessage(ctx context.Context, portal *bridgev2.Portal, state *streamingState, model string, finishReason string) {
	if portal == nil || state == nil || !state.hasEditTarget() {
		return
	}
	log := cc.loggerForContext(ctx)

	fullMeta := &MessageMetadata{
		BaseMessageMetadata: agentremote.BuildAssistantBaseMetadata(agentremote.AssistantMetadataParams{
			Body:               state.accumulated.String(),
			FinishReason:       finishReason,
			TurnID:             state.turnID,
			AgentID:            state.agentID,
			ToolCalls:          state.toolCalls,
			StartedAtMs:        state.startedAtMs,
			CompletedAtMs:      state.completedAtMs,
			CanonicalSchema:    "ai-sdk-ui-message-v1",
			CanonicalUIMessage: cc.buildCanonicalUIMessage(state, model, finishReason),
			GeneratedFiles:     agentremote.GeneratedFileRefsFromParts(state.generatedFiles),
			ThinkingContent:    state.reasoning.String(),
			PromptTokens:       state.promptTokens,
			CompletionTokens:   state.completionTokens,
			ReasoningTokens:    state.reasoningTokens,
		}),
		Model:              model,
		FirstTokenAtMs:     state.firstTokenAtMs,
		HasToolCalls:       len(state.toolCalls) > 0,
		ThinkingTokenCount: len(strings.Fields(state.reasoning.String())),
	}

	agentremote.UpsertAssistantMessage(ctx, agentremote.UpsertAssistantMessageParams{
		Login:            cc.UserLogin,
		Portal:           portal,
		SenderID:         codexGhostID,
		NetworkMessageID: state.networkMessageID,
		InitialEventID:   state.initialEventID,
		Metadata:         fullMeta,
		Logger:           *log,
	})
}

func (cc *CodexClient) buildSDKFinalMetadata(turn *bridgesdk.Turn, state *streamingState, model string, finishReason string) any {
	if turn == nil || state == nil {
		return &MessageMetadata{}
	}
	return &MessageMetadata{
		BaseMessageMetadata: agentremote.BuildAssistantBaseMetadata(agentremote.AssistantMetadataParams{
			Body:               state.accumulated.String(),
			FinishReason:       finishReason,
			TurnID:             turn.ID(),
			AgentID:            state.agentID,
			ToolCalls:          state.toolCalls,
			StartedAtMs:        state.startedAtMs,
			CompletedAtMs:      state.completedAtMs,
			CanonicalSchema:    "ai-sdk-ui-message-v1",
			CanonicalUIMessage: streamui.SnapshotCanonicalUIMessage(turn.UIState()),
			GeneratedFiles:     agentremote.GeneratedFileRefsFromParts(state.generatedFiles),
			ThinkingContent:    state.reasoning.String(),
			PromptTokens:       state.promptTokens,
			CompletionTokens:   state.completionTokens,
			ReasoningTokens:    state.reasoningTokens,
		}),
		Model:              model,
		FirstTokenAtMs:     state.firstTokenAtMs,
		HasToolCalls:       len(state.toolCalls) > 0,
		ThinkingTokenCount: len(strings.Fields(state.reasoning.String())),
	}
}

// --- Approvals ---

// pendingToolApprovalDataCodex holds codex-specific metadata stored in
// ApprovalFlow's Pending.Data field.
type pendingToolApprovalDataCodex struct {
	ApprovalID   string
	RoomID       id.RoomID
	ToolCallID   string
	ToolName     string
	Presentation agentremote.ApprovalPromptPresentation
}

type codexSDKApprovalHandle struct {
	client     *CodexClient
	portal     *bridgev2.Portal
	state      *streamingState
	approvalID string
	toolCallID string
}

func (h *codexSDKApprovalHandle) ID() string {
	if h == nil {
		return ""
	}
	return h.approvalID
}

func (h *codexSDKApprovalHandle) ToolCallID() string {
	if h == nil {
		return ""
	}
	return h.toolCallID
}

func (h *codexSDKApprovalHandle) Wait(ctx context.Context) (bridgesdk.ToolApprovalResponse, error) {
	if h == nil || h.client == nil {
		return bridgesdk.ToolApprovalResponse{}, nil
	}
	decision, ok := h.client.waitToolApproval(ctx, h.approvalID)
	reason := strings.TrimSpace(decision.Reason)
	if reason == "" {
		reason = agentremote.ApprovalReasonTimeout
		if ctx != nil && ctx.Err() != nil {
			reason = agentremote.ApprovalReasonCancelled
		}
	}
	if h.portal != nil {
		h.client.uiEmitter(h.state).EmitUIToolApprovalResponse(ctx, h.portal, h.approvalID, h.toolCallID, ok && decision.Approved, reason)
		if !(ok && decision.Approved) {
			h.client.uiEmitter(h.state).EmitUIToolOutputDenied(ctx, h.portal, h.toolCallID)
		}
	}
	return bridgesdk.ToolApprovalResponse{
		Approved: ok && decision.Approved,
		Always:   decision.Always,
		Reason:   reason,
	}, nil
}

func (cc *CodexClient) requestSDKApproval(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	turn *bridgesdk.Turn,
	req bridgesdk.ApprovalRequest,
) bridgesdk.ApprovalHandle {
	if cc == nil || portal == nil {
		return &codexSDKApprovalHandle{toolCallID: req.ToolCallID}
	}
	approvalID := strings.TrimSpace(req.ApprovalID)
	if approvalID == "" {
		approvalID = fmt.Sprintf("codex-%d", time.Now().UnixNano())
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = agentremote.DefaultApprovalExpiry
	}
	presentation := agentremote.ApprovalPromptPresentation{
		Title:       req.ToolName,
		AllowAlways: false,
	}
	if req.Presentation != nil {
		presentation = *req.Presentation
	}
	cc.setApprovalStateTracking(state, approvalID, req.ToolCallID, req.ToolName)
	cc.registerToolApproval(portal.MXID, approvalID, req.ToolCallID, req.ToolName, presentation, ttl)
	if turn != nil {
		turn.Emitter().EmitUIToolApprovalRequest(turn.Context(), portal, approvalID, req.ToolCallID)
		cc.approvalFlow.SendPrompt(turn.Context(), portal, agentremote.SendPromptParams{
			ApprovalPromptMessageParams: agentremote.ApprovalPromptMessageParams{
				ApprovalID:   approvalID,
				ToolCallID:   req.ToolCallID,
				ToolName:     req.ToolName,
				TurnID:       turn.ID(),
				Presentation: presentation,
				ExpiresAt:    time.Now().Add(ttl),
			},
			RoomID:    portal.MXID,
			OwnerMXID: cc.UserLogin.UserMXID,
		})
	} else {
		cc.emitUIToolApprovalRequest(ctx, portal, state, approvalID, req.ToolCallID, req.ToolName, presentation, int(ttl/time.Second))
	}
	return &codexSDKApprovalHandle{
		client:     cc,
		portal:     portal,
		state:      state,
		approvalID: approvalID,
		toolCallID: req.ToolCallID,
	}
}

func (cc *CodexClient) registerToolApproval(
	roomID id.RoomID,
	approvalID, toolCallID, toolName string,
	presentation agentremote.ApprovalPromptPresentation,
	ttl time.Duration,
) (*agentremote.Pending[*pendingToolApprovalDataCodex], bool) {
	data := &pendingToolApprovalDataCodex{
		ApprovalID:   strings.TrimSpace(approvalID),
		RoomID:       roomID,
		ToolCallID:   strings.TrimSpace(toolCallID),
		ToolName:     strings.TrimSpace(toolName),
		Presentation: presentation,
	}
	return cc.approvalFlow.Register(approvalID, ttl, data)
}

func (cc *CodexClient) waitToolApproval(ctx context.Context, approvalID string) (agentremote.ApprovalDecisionPayload, bool) {
	approvalID = strings.TrimSpace(approvalID)
	decision, ok := cc.approvalFlow.Wait(ctx, approvalID)
	if !ok {
		reason := agentremote.ApprovalReasonTimeout
		if ctx.Err() != nil {
			reason = agentremote.ApprovalReasonCancelled
		}
		decision = agentremote.ApprovalDecisionPayload{
			ApprovalID: approvalID,
			Reason:     reason,
		}
		cc.approvalFlow.FinishResolved(approvalID, decision)
		return decision, false
	}
	cc.approvalFlow.FinishResolved(approvalID, decision)
	return decision, true
}

func (cc *CodexClient) handleApprovalRequest(
	ctx context.Context, req codexrpc.Request,
	defaultToolName string,
	extractInput func(json.RawMessage) (map[string]any, agentremote.ApprovalPromptPresentation),
) (any, *codexrpc.RPCError) {
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
	approvalID := strings.Trim(strings.TrimSpace(string(req.ID)), "\"")

	inputMap, presentation := extractInput(req.Params)
	cc.ensureUIToolInputStart(ctx, active.portal, active.state, toolCallID, toolName, true, inputMap)
	handle := cc.requestSDKApproval(ctx, active.portal, active.state, active.state.turn, bridgesdk.ApprovalRequest{
		ApprovalID:   approvalID,
		ToolCallID:   toolCallID,
		ToolName:     toolName,
		TTL:          10 * time.Minute,
		Presentation: &presentation,
	})

	if active.meta != nil {
		if lvl, _ := stringutil.NormalizeElevatedLevel(active.meta.ElevatedLevel); lvl == "full" {
			_ = cc.approvalFlow.Resolve(handle.ID(), agentremote.ApprovalDecisionPayload{
				ApprovalID: handle.ID(),
				Approved:   true,
				Reason:     "auto-approved",
			})
		}
	}

	decision, err := handle.Wait(ctx)
	if err != nil {
		return map[string]any{"decision": "decline"}, nil
	}
	if decision.Approved {
		return map[string]any{"decision": "accept"}, nil
	}
	return map[string]any{"decision": "decline"}, nil
}

func (cc *CodexClient) handleCommandApprovalRequest(ctx context.Context, req codexrpc.Request) (any, *codexrpc.RPCError) {
	return cc.handleApprovalRequest(ctx, req, "commandExecution", func(raw json.RawMessage) (map[string]any, agentremote.ApprovalPromptPresentation) {
		var p struct {
			Command *string `json:"command"`
			Cwd     *string `json:"cwd"`
			Reason  *string `json:"reason"`
		}
		_ = json.Unmarshal(raw, &p)
		input := map[string]any{}
		details := make([]agentremote.ApprovalDetail, 0, 3)
		input, details = agentremote.AddOptionalDetail(input, details, "command", "Command", p.Command)
		input, details = agentremote.AddOptionalDetail(input, details, "cwd", "Working directory", p.Cwd)
		input, details = agentremote.AddOptionalDetail(input, details, "reason", "Reason", p.Reason)
		return input, agentremote.ApprovalPromptPresentation{
			Title:       "Codex command execution",
			Details:     details,
			AllowAlways: false,
		}
	})
}

func (cc *CodexClient) handleFileChangeApprovalRequest(ctx context.Context, req codexrpc.Request) (any, *codexrpc.RPCError) {
	return cc.handleApprovalRequest(ctx, req, "fileChange", func(raw json.RawMessage) (map[string]any, agentremote.ApprovalPromptPresentation) {
		var p struct {
			Reason    *string `json:"reason"`
			GrantRoot *string `json:"grantRoot"`
		}
		_ = json.Unmarshal(raw, &p)
		input := map[string]any{}
		details := make([]agentremote.ApprovalDetail, 0, 2)
		input, details = agentremote.AddOptionalDetail(input, details, "grantRoot", "Grant root", p.GrantRoot)
		input, details = agentremote.AddOptionalDetail(input, details, "reason", "Reason", p.Reason)
		return input, agentremote.ApprovalPromptPresentation{
			Title:       "Codex file change",
			Details:     details,
			AllowAlways: false,
		}
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
