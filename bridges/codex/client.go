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
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/bridges/codex/codexrpc"
	"github.com/beeper/agentremote/pkg/matrixevents"
	"github.com/beeper/agentremote/pkg/shared/bridgeutil"
	"github.com/beeper/agentremote/pkg/shared/citations"
	"github.com/beeper/agentremote/pkg/shared/streamui"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/sdk"
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
const aiCapabilityID = "com.beeper.ai.v1"

var aiBaseCaps = sdk.BuildRoomFeatures(sdk.RoomFeaturesParams{
	ID:                  aiCapabilityID,
	MaxTextLength:       100000,
	Reply:               event.CapLevelFullySupported,
	Thread:              event.CapLevelFullySupported,
	Edit:                event.CapLevelFullySupported,
	Reaction:            event.CapLevelFullySupported,
	ReadReceipts:        true,
	TypingNotifications: true,
	DeleteChat:          true,
})

func humanUserID(loginID networkid.UserLoginID) networkid.UserID {
	return sdk.HumanUserID("codex-user", loginID)
}

const AIAuthFailed status.BridgeStateErrorCode = "ai-auth-failed"

func messageStatusForError(_ error) event.MessageStatus {
	return event.MessageStatusRetriable
}

func messageStatusReasonForError(_ error) event.MessageStatusReason {
	return event.MessageStatusGenericError
}

func messageSendStatusError(err error, message string, reason event.MessageStatusReason) error {
	return sdk.MessageSendStatusError(err, message, reason, messageStatusForError, messageStatusReasonForError)
}

type codexNotif struct {
	Method string
	Params json.RawMessage
}

func codexTurnKey(threadID, turnID string) string {
	return strings.TrimSpace(threadID) + "\n" + strings.TrimSpace(turnID)
}

type codexActiveTurn struct {
	portal      *bridgev2.Portal
	portalState *codexPortalState
	streamState *streamingState
	threadID    string
	turnID      string
	model       string
}

type codexPendingMessage struct {
	event  *event.Event
	portal *bridgev2.Portal
	state  *codexPortalState
	body   string
}

type codexPendingQueue []*codexPendingMessage

type CodexClient struct {
	sdk.ClientBase
	UserLogin *bridgev2.UserLogin
	connector *CodexConnector
	log       zerolog.Logger

	defaultChatMu sync.Mutex // serializes default-room bootstrap and welcome notices
	rpcMu         sync.Mutex
	rpc           *codexrpc.Client

	notifCh   chan codexNotif
	notifDone chan struct{} // closed on Disconnect to stop dispatchNotifications

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

	approvalFlow *sdk.ApprovalFlow[*pendingToolApprovalDataCodex]

	scheduleBootstrapOnce func() // starts bootstrap goroutine exactly once

	roomMu          sync.Mutex
	activeRooms     map[id.RoomID]bool
	pendingMessages map[id.RoomID]codexPendingQueue
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
	cc.HumanUserIDPrefix = "codex-user"
	cc.MessageIDPrefix = "codex"
	cc.MessageLogKey = "codex_msg_id"
	cc.approvalFlow = sdk.NewApprovalFlow(sdk.ApprovalFlowConfig[*pendingToolApprovalDataCodex]{
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
	return sdk.LoggerFromContext(ctx, &cc.log)
}

func (cc *CodexClient) Connect(ctx context.Context) {
	cc.SetLoggedIn(false)
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
		cc.SetLoggedIn(true)
		meta := loginMetadata(cc.UserLogin)
		if strings.TrimSpace(resp.Account.Email) != "" {
			meta.CodexAccountEmail = strings.TrimSpace(resp.Account.Email)
			_ = cc.UserLogin.Save(cc.backgroundContext(ctx))
		}
	}
	if resp.Account == nil {
		state := status.StateBadCredentials
		message := "Codex login is no longer authenticated."
		if isHostAuthLogin(loginMetadata(cc.UserLogin)) {
			state = status.StateTransientDisconnect
			message = "Codex host authentication is unavailable."
		}
		cc.UserLogin.BridgeState.Send(status.BridgeState{
			StateEvent: state,
			Error:      AIAuthFailed,
			Message:    message,
		})
		return
	}

	cc.UserLogin.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateConnected,
		Message:    "Connected",
	})
}

func (cc *CodexClient) Disconnect() {
	cc.SetLoggedIn(false)
	if cc.approvalFlow != nil {
		cc.approvalFlow.Close()
	}

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

func (cc *CodexClient) GetUserLogin() *bridgev2.UserLogin { return cc.UserLogin }

func (cc *CodexClient) GetApprovalHandler() sdk.ApprovalReactionHandler {
	return cc.approvalFlow
}

func (cc *CodexClient) senderForPortal() bridgev2.EventSender {
	if cc == nil || cc.UserLogin == nil {
		return bridgev2.EventSender{Sender: codexGhostID}
	}
	return bridgev2.EventSender{Sender: codexGhostID, SenderLogin: cc.UserLogin.ID}
}

func (cc *CodexClient) senderForHuman() bridgev2.EventSender {
	if cc == nil || cc.UserLogin == nil {
		return bridgev2.EventSender{IsFromMe: true}
	}
	return bridgev2.EventSender{Sender: cc.HumanUserID(), SenderLogin: cc.UserLogin.ID, IsFromMe: true}
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
		sdk.RemoveClientFromCache(&cc.connector.clientsMu, cc.connector.clients, cc.UserLogin.ID)
	}

	cc.UserLogin.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateLoggedOut,
		Message:    "Disconnected by user",
	})
}

func (cc *CodexClient) purgeCodexHomeBestEffort(_ context.Context) {
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
	records, err := listCodexPortalStateRecords(ctx, cc.UserLogin)
	if err != nil || len(records) == 0 {
		return
	}

	tmp := filepath.Clean(os.TempDir())
	if tmp == "" || tmp == "." || tmp == string(os.PathSeparator) {
		// Should never happen, but avoid deleting arbitrary dirs if it does.
		return
	}

	seen := make(map[string]struct{})
	for _, record := range records {
		if record.State == nil {
			continue
		}
		cwd := strings.TrimSpace(record.State.CodexCwd)
		if cwd == "" {
			continue
		}
		clean := filepath.Clean(cwd)
		if clean == "." || clean == string(os.PathSeparator) {
			continue
		}
		// Safety: only delete dirs we created in agentremote-codex temp roots.
		if !isManagedCodexTempDirPath(clean) {
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

func isManagedCodexTempDirPath(path string) bool {
	for _, segment := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if segment == "agentremote-codex" || strings.HasPrefix(segment, "agentremote-codex-") {
			return true
		}
	}
	return false
}

func (cc *CodexClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	meta := portalMeta(portal)
	if meta == nil || !meta.IsCodexRoom {
		return bridgeutil.BuildChatInfoWithFallback("", portal.Name, "Codex", portal.Topic), nil
	}
	state, err := loadCodexPortalState(ctx, portal)
	if err != nil {
		return nil, err
	}
	return cc.composeCodexChatInfo(portal, state, strings.TrimSpace(state.CodexThreadID) != ""), nil
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
		portal, err := cc.createWelcomeCodexChat(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to ensure Codex chat: %w", err)
		}
		if portal == nil {
			return nil, errors.New("codex chat unavailable")
		}
		state, err := loadCodexPortalState(ctx, portal)
		if err != nil {
			return nil, fmt.Errorf("failed to load Codex room state: %w", err)
		}
		chatInfo := cc.composeCodexChatInfo(portal, state, strings.TrimSpace(state.CodexThreadID) != "")
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

func isCodexIdentifier(identifier string) bool {
	switch strings.ToLower(strings.TrimSpace(identifier)) {
	case "codex", "@codex", "codex:default", "codex:codex":
		return true
	default:
		return false
	}
}

func (cc *CodexClient) GetContactList(ctx context.Context) ([]*bridgev2.ResolveIdentifierResponse, error) {
	resp, err := cc.ResolveIdentifier(ctx, "codex", false)
	if err != nil {
		return nil, err
	}
	return []*bridgev2.ResolveIdentifierResponse{resp}, nil
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
		return nil, sdk.UnsupportedMessageStatus(errors.New("not a Codex room"))
	}
	state, err := loadCodexPortalState(ctx, portal)
	if err != nil {
		return nil, err
	}
	if sdk.IsMatrixBotUser(ctx, cc.UserLogin.Bridge, msg.Event.Sender) {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	// Only text messages.
	switch msg.Content.MsgType {
	case event.MsgText, event.MsgNotice, event.MsgEmote:
	default:
		return nil, sdk.UnsupportedMessageStatus(fmt.Errorf("%s messages are not supported", msg.Content.MsgType))
	}
	if msg.Content.RelatesTo != nil && msg.Content.RelatesTo.GetReplaceID() != "" {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	body := strings.TrimSpace(msg.Content.Body)
	if body == "" {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	if res, handled, err := cc.handleCodexCommand(ctx, portal, state, body); handled {
		return res, err
	}

	if state.AwaitingCwdSetup {
		return cc.handleWelcomeCodexMessage(ctx, portal, state, body)
	}

	if err := cc.ensureRPC(cc.backgroundContext(ctx)); err != nil {
		return nil, messageSendStatusError(err, "Codex isn't available. Sign in again.", "")
	}
	if strings.TrimSpace(state.CodexThreadID) == "" || strings.TrimSpace(state.CodexCwd) == "" {
		if err := cc.ensureCodexThread(ctx, portal, state); err != nil {
			return nil, messageSendStatusError(err, "Codex thread unavailable. Try !ai reset.", "")
		}
	}
	if err := cc.ensureCodexThreadLoaded(ctx, portal, state); err != nil {
		return nil, messageSendStatusError(err, "Codex thread unavailable. Try !ai reset.", "")
	}

	roomID := portal.MXID
	if roomID == "" {
		return nil, errors.New("portal has no room id")
	}

	// Save user message immediately; we return Pending=true.
	userMsg := &database.Message{
		ID:        sdk.MatrixMessageID(msg.Event.ID),
		MXID:      msg.Event.ID,
		Room:      portal.PortalKey,
		SenderID:  humanUserID(cc.UserLogin.ID),
		Timestamp: sdk.MatrixEventTimestamp(msg.Event),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: sdk.BaseMessageMetadata{Role: "user", Body: body},
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
		bridgeutil.SendMessageStatus(ctx, portal, msg.Event, bridgev2.MessageStatus{
			Status:    event.MessageStatusPending,
			Message:   "Queued — waiting for current turn to finish...",
			IsCertain: true,
		})
		cc.queuePendingCodex(roomID, &codexPendingMessage{
			event:  msg.Event,
			portal: portal,
			state:  state,
			body:   body,
		})
		return &bridgev2.MatrixMessageResponse{
			DB:      userMsg,
			Pending: true,
		}, nil
	}

	bridgeutil.SendMessageStatus(ctx, portal, msg.Event, bridgev2.MessageStatus{
		Status:    event.MessageStatusPending,
		Message:   "Processing...",
		IsCertain: true,
	})

	go func() {
		func() {
			defer cc.releaseRoom(roomID)
			cc.runTurn(cc.backgroundContext(ctx), portal, state, msg.Event, body)
		}()
		cc.processPendingCodex(roomID)
	}()

	return &bridgev2.MatrixMessageResponse{
		DB:      userMsg,
		Pending: true,
	}, nil
}

func (cc *CodexClient) runTurn(ctx context.Context, portal *bridgev2.Portal, portalState *codexPortalState, sourceEvent *event.Event, body string) {
	log := cc.loggerForContext(ctx)
	streamState := newStreamingState(sourceEvent.ID)

	model := cc.connector.Config.Codex.DefaultModel
	streamState.currentModel = model
	threadID := strings.TrimSpace(portalState.CodexThreadID)
	cwd := strings.TrimSpace(portalState.CodexCwd)
	conv := sdk.NewConversation(ctx, cc.UserLogin, portal, cc.senderForPortal(), cc.connector.sdkConfig, cc)
	source := sdk.UserMessageSource(sourceEvent.ID.String())
	turn := conv.StartTurn(ctx, codexSDKAgent(), source)
	approvals := turn.Approvals()
	if cc.streamEventHook != nil {
		turn.SetStreamHook(func(turnID string, seq int, content map[string]any, txnID string) bool {
			cc.streamEventHook(turnID, seq, content, txnID)
			return true
		})
	}
	approvals.SetHandler(func(callCtx context.Context, sdkTurn *sdk.Turn, req sdk.ApprovalRequest) sdk.ApprovalHandle {
		return cc.requestSDKApproval(callCtx, portal, streamState, sdkTurn, req)
	})
	turn.SetFinalMetadataProvider(sdk.FinalMetadataProviderFunc(func(sdkTurn *sdk.Turn, finishReason string) any {
		return cc.buildSDKFinalMetadata(sdkTurn, streamState, codexStateModel(streamState, model), finishReason)
	}))
	streamState.turn = turn
	streamState.agentID = string(codexGhostID)
	turn.Writer().MessageMetadata(ctx, cc.buildUIMessageMetadata(streamState, codexStateModel(streamState, model), false, ""))
	turn.Writer().StepStart(ctx)

	approvalPolicy := "untrusted"
	if lvl, _ := stringutil.NormalizeElevatedLevel(portalState.ElevatedLevel); lvl == "full" {
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
		turn.EndWithError("Codex turn/start response missing turn id")
		return
	}
	bridgeutil.SendMessageStatus(ctx, portal, sourceEvent, bridgev2.MessageStatus{
		Status:    event.MessageStatusSuccess,
		IsCertain: true,
	})

	turnCh := cc.subscribeTurn(threadID, turnID)
	defer cc.unsubscribeTurn(threadID, turnID)

	cc.activeMu.Lock()
	cc.activeTurns[codexTurnKey(threadID, turnID)] = &codexActiveTurn{
		portal:      portal,
		portalState: portalState,
		streamState: streamState,
		threadID:    threadID,
		turnID:      turnID,
		model:       model,
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
			cc.handleNotif(ctx, portal, portalState, streamState, model, threadID, turnID, evt)
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
	streamState.completedAtMs = time.Now().UnixMilli()
	// If we observed turn-level diff updates, finalize them as a dedicated tool output.
	if diff := strings.TrimSpace(streamState.codexLatestDiff); diff != "" {
		diffToolID := fmt.Sprintf("diff-%s", turnID)
		emitDiffToolOutput(ctx, streamState, diffToolID, turnID, diff, false)
		streamState.toolCalls = append(streamState.toolCalls, ToolCallMetadata{
			CallID:        diffToolID,
			ToolName:      "diff",
			ToolType:      string(matrixevents.ToolTypeProvider),
			Input:         map[string]any{"turnId": turnID},
			Output:        map[string]any{"diff": diff},
			Status:        string(matrixevents.ToolStatusCompleted),
			ResultStatus:  string(matrixevents.ResultStatusSuccess),
			StartedAtMs:   streamState.startedAtMs,
			CompletedAtMs: streamState.completedAtMs,
		})
	}
	if completedErr != "" {
		streamState.turn.Writer().MessageMetadata(ctx, cc.buildUIMessageMetadata(streamState, codexStateModel(streamState, model), true, finishStatus))
		streamState.turn.EndWithError(completedErr)
		return
	}
	streamState.turn.Writer().MessageMetadata(ctx, cc.buildUIMessageMetadata(streamState, codexStateModel(streamState, model), true, finishStatus))
	streamState.turn.End(finishStatus)
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

// emitDiffToolOutput emits a diff tool output via the SDK writer.
func emitDiffToolOutput(ctx context.Context, state *streamingState, diffToolID, turnID, diff string, streaming bool) {
	if state == nil || state.turn == nil {
		return
	}
	state.turn.Writer().Tools().EnsureInputStart(ctx, diffToolID, map[string]any{"turnId": turnID}, sdk.ToolInputOptions{
		ToolName:         "diff",
		ProviderExecuted: true,
	})
	state.turn.Writer().Tools().Output(ctx, diffToolID, diff, sdk.ToolOutputOptions{
		ProviderExecuted: true,
		Streaming:        streaming,
	})
}

func codexStateModel(state *streamingState, fallback string) string {
	if state != nil {
		if model := strings.TrimSpace(state.currentModel); model != "" {
			return model
		}
	}
	return strings.TrimSpace(fallback)
}

// codexNotifFields holds the common fields present in most Codex notifications.
type codexNotifFields struct {
	Delta  string `json:"delta"`
	ItemID string `json:"itemId"`
	Thread string `json:"threadId"`
	Turn   string `json:"turnId"`
}

// parseNotifFields unmarshals common fields and returns false if the notification
// does not belong to the given thread/turn pair.
func parseNotifFields(params json.RawMessage, threadID, turnID string) (codexNotifFields, bool) {
	var f codexNotifFields
	_ = json.Unmarshal(params, &f)
	return f, f.Thread == threadID && f.Turn == turnID
}

var codexSimpleOutputDeltaMethods = map[string]string{
	"item/commandExecution/outputDelta": "commandExecution",
	"item/fileChange/outputDelta":       "fileChange",
	"item/collabToolCall/outputDelta":   "collabToolCall",
	"item/plan/delta":                   "plan",
}

type toolNameExtractor func(json.RawMessage) (name string, inputKey string)

func (cc *CodexClient) handleSimpleOutputDelta(
	ctx context.Context, state *streamingState,
	params json.RawMessage, threadID, turnID, defaultToolName string,
	extractName toolNameExtractor,
) {
	f, ok := parseNotifFields(params, threadID, turnID)
	if !ok {
		return
	}
	toolName := defaultToolName
	inputMap := map[string]any{}
	if extractName != nil {
		if name, key := extractName(params); name != "" {
			toolName = name
			if key != "" {
				inputMap[key] = name
			}
		}
	}
	toolCallID := strings.TrimSpace(f.ItemID)
	if toolCallID == "" {
		toolCallID = toolName
	}
	buf := cc.appendCodexToolOutput(state, toolCallID, f.Delta)
	if state.turn != nil {
		state.turn.Writer().Tools().EnsureInputStart(ctx, toolCallID, inputMap, sdk.ToolInputOptions{
			ToolName:         toolName,
			ProviderExecuted: true,
		})
		state.turn.Writer().Tools().Output(ctx, toolCallID, buf, sdk.ToolOutputOptions{
			ProviderExecuted: true,
			Streaming:        true,
		})
	}
}

func (cc *CodexClient) handleNotif(ctx context.Context, portal *bridgev2.Portal, portalState *codexPortalState, state *streamingState, model, threadID, turnID string, evt codexNotif) {
	if defaultToolName, ok := codexSimpleOutputDeltaMethods[evt.Method]; ok {
		cc.handleSimpleOutputDelta(ctx, state, evt.Params, threadID, turnID, defaultToolName, nil)
		return
	}
	parseFields := func() (codexNotifFields, bool) {
		return parseNotifFields(evt.Params, threadID, turnID)
	}
	appendReasoningDelta := func(delta string) {
		state.recordFirstToken()
		state.reasoning.WriteString(delta)
		if state.turn != nil {
			state.turn.Writer().ReasoningDelta(ctx, delta)
		}
	}
	switch evt.Method {
	case "error":
		var p struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(evt.Params, &p)
		if strings.TrimSpace(p.Error.Message) != "" {
			if state.turn != nil {
				state.turn.Writer().Error(ctx, p.Error.Message)
			}
			cc.sendSystemNoticeOnce(ctx, portal, state, "turn:error", "Codex error: "+strings.TrimSpace(p.Error.Message))
		}
	case "item/agentMessage/delta":
		f, ok := parseFields()
		if !ok {
			return
		}
		state.recordFirstToken()
		state.accumulated.WriteString(f.Delta)
		if state.turn != nil {
			state.turn.Writer().TextDelta(ctx, f.Delta)
		}
	case "item/reasoning/summaryTextDelta":
		f, ok := parseFields()
		if !ok {
			return
		}
		state.codexReasoningSummarySeen = true
		appendReasoningDelta(f.Delta)
	case "item/reasoning/summaryPartAdded":
		if _, ok := parseFields(); !ok {
			return
		}
		state.codexReasoningSummarySeen = true
		if state.reasoning.Len() > 0 {
			state.reasoning.WriteString("\n")
			if state.turn != nil {
				state.turn.Writer().ReasoningDelta(ctx, "\n")
			}
		}
	case "item/reasoning/textDelta":
		f, ok := parseFields()
		if !ok || state.codexReasoningSummarySeen {
			// Prefer summary deltas when present to avoid duplicate reasoning output.
			return
		}
		appendReasoningDelta(f.Delta)
	case "item/mcpToolCall/outputDelta":
		cc.handleSimpleOutputDelta(ctx, state, evt.Params, threadID, turnID, "mcpToolCall", func(raw json.RawMessage) (string, string) {
			var extra struct {
				Tool string `json:"tool"`
			}
			_ = json.Unmarshal(raw, &extra)
			if name := strings.TrimSpace(extra.Tool); name != "" {
				return name, "tool"
			}
			return "", ""
		})
	case "model/rerouted":
		f, ok := parseFields()
		if !ok {
			return
		}
		var p struct {
			ToModel string `json:"toModel"`
		}
		_ = json.Unmarshal(evt.Params, &p)
		nextModel := strings.TrimSpace(p.ToModel)
		if nextModel == "" {
			return
		}
		state.currentModel = nextModel
		if state.turn != nil {
			state.turn.Writer().MessageMetadata(ctx, cc.buildUIMessageMetadata(state, nextModel, true, ""))
		}
		cc.activeMu.Lock()
		if active := cc.activeTurns[codexTurnKey(f.Thread, f.Turn)]; active != nil {
			active.model = nextModel
		}
		cc.activeMu.Unlock()
	case "turn/diff/updated":
		if _, ok := parseFields(); !ok {
			return
		}
		var diffPayload struct {
			Diff string `json:"diff"`
		}
		_ = json.Unmarshal(evt.Params, &diffPayload)
		state.codexLatestDiff = diffPayload.Diff
		emitDiffToolOutput(ctx, state, fmt.Sprintf("diff-%s", turnID), turnID, diffPayload.Diff, true)
	case "turn/plan/updated":
		if _, ok := parseFields(); !ok {
			return
		}
		var p struct {
			Explanation *string          `json:"explanation"`
			Plan        []map[string]any `json:"plan"`
		}
		_ = json.Unmarshal(evt.Params, &p)
		toolCallID := fmt.Sprintf("turn-plan-%s", turnID)
		input := map[string]any{}
		if p.Explanation != nil && strings.TrimSpace(*p.Explanation) != "" {
			input["explanation"] = strings.TrimSpace(*p.Explanation)
		}
		if state.turn != nil {
			state.turn.Writer().Tools().EnsureInputStart(ctx, toolCallID, input, sdk.ToolInputOptions{
				ToolName:         "plan",
				ProviderExecuted: true,
			})
			state.turn.Writer().Tools().Output(ctx, toolCallID, map[string]any{
				"explanation": input["explanation"],
				"plan":        p.Plan,
			}, sdk.ToolOutputOptions{
				ProviderExecuted: true,
				Streaming:        true,
			})
		}
		cc.sendSystemNoticeOnce(ctx, portal, state, "turn:plan_updated", "Codex updated the plan.")
	case "thread/tokenUsage/updated":
		if _, ok := parseFields(); !ok {
			return
		}
		var p struct {
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
		state.promptTokens = p.TokenUsage.Total.InputTokens + p.TokenUsage.Total.CachedInputTokens
		state.completionTokens = p.TokenUsage.Total.OutputTokens
		state.reasoningTokens = p.TokenUsage.Total.ReasoningOutputTokens
		state.totalTokens = p.TokenUsage.Total.TotalTokens
		if state.turn != nil {
			state.turn.Writer().MessageMetadata(ctx, cc.buildUIMessageMetadata(state, codexStateModel(state, model), true, ""))
		}
	case "item/started", "item/completed":
		if _, ok := parseFields(); !ok {
			return
		}
		var p struct {
			Item json.RawMessage `json:"item"`
		}
		_ = json.Unmarshal(evt.Params, &p)
		if evt.Method == "item/started" {
			cc.handleItemStarted(ctx, portal, state, p.Item)
		} else {
			cc.handleItemCompleted(ctx, portal, state, p.Item)
		}
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

	if state.turn != nil {
		state.turn.Writer().Tools().EnsureInputStart(ctx, itemID, it, sdk.ToolInputOptions{
			ToolName:         toolName,
			ProviderExecuted: true,
		})
	}

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

func (cc *CodexClient) emitNewArtifacts(ctx context.Context, portal *bridgev2.Portal, state *streamingState, docs []citations.SourceDocument, files []citations.GeneratedFilePart) {
	for _, document := range docs {
		if state.turn != nil {
			state.turn.Writer().SourceDocument(ctx, document)
		}
	}
	for _, file := range files {
		if state.turn != nil {
			state.turn.Writer().File(ctx, file.URL, file.MediaType)
		}
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
		if state.turn != nil {
			state.turn.Writer().TextDelta(ctx, it.Text)
		}
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
		if state.turn != nil {
			state.turn.Writer().ReasoningDelta(ctx, text)
		}
		return
	case "commandExecution", "fileChange", "mcpToolCall":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		statusVal, _ := it["status"].(string)
		statusVal = strings.TrimSpace(statusVal)
		errText := extractItemErrorMessage(it)
		switch statusVal {
		case "declined":
			if state.turn != nil {
				state.turn.Writer().Tools().Denied(ctx, itemID)
			}
		case "failed":
			if state.turn != nil {
				state.turn.Writer().Tools().OutputError(ctx, itemID, errText, true)
			}
		default:
			if state.turn != nil {
				state.turn.Writer().Tools().Output(ctx, itemID, it, sdk.ToolOutputOptions{
					ProviderExecuted: true,
				})
			}
		}
		newDocs, newFiles := collectToolOutputArtifacts(state, it)
		cc.emitNewArtifacts(ctx, portal, state, newDocs, newFiles)

		tc := newProviderToolCall(itemID, fmt.Sprintf("%v", it["type"]), it)
		switch statusVal {
		case "declined":
			tc.ResultStatus = string(matrixevents.ResultStatusDenied)
			tc.ErrorMessage = "Denied by user"
		case "failed":
			tc.ResultStatus = string(matrixevents.ResultStatusError)
			tc.ErrorMessage = errText
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

func extractItemErrorMessage(it map[string]any) string {
	if errObj, ok := it["error"].(map[string]any); ok {
		if msg, ok := errObj["message"].(string); ok && strings.TrimSpace(msg) != "" {
			return strings.TrimSpace(msg)
		}
	}
	return "tool failed"
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
	if state.turn != nil {
		state.turn.Writer().Tools().Output(ctx, itemID, it, sdk.ToolOutputOptions{
			ProviderExecuted: true,
		})
	}
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
				if state.turn != nil {
					state.turn.Writer().SourceURL(ctx, citation)
				}
			}
		}
	}
	if opts.collectArtifacts {
		newDocs, newFiles := collectToolOutputArtifacts(state, it)
		cc.emitNewArtifacts(ctx, portal, state, newDocs, newFiles)
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
	if state.turn != nil {
		state.turn.Writer().Tools().Output(ctx, itemID, text, sdk.ToolOutputOptions{
			ProviderExecuted: true,
		})
	}
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
	_, err = rpc.InitializeWithOptions(initCtx, codexrpc.ClientInfo{Name: ci.Name, Title: ci.Title, Version: ci.Version}, codexrpc.InitializeOptions{
		// Thread recovery uses persistExtendedHistory, which currently requires
		// the experimental API capability during initialize.
		ExperimentalAPI: true,
	})
	if err != nil {
		_ = rpc.Close()
		cc.rpc = nil
		return err
	}

	cc.startDispatching()

	rpc.OnNotification(func(method string, params json.RawMessage) {
		if !cc.IsLoggedIn() {
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
	rpc.HandleRequest("item/permissions/requestApproval", cc.handlePermissionsApprovalRequest)

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
			cc.SetLoggedIn(p.AuthMode != nil && strings.TrimSpace(*p.AuthMode) != "")
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
		if evt.Method == "turn/completed" {
			cc.activeMu.Lock()
			if active := cc.activeTurns[key]; active != nil && (active.streamState == nil || active.streamState.turn == nil) {
				delete(cc.activeTurns, key)
			}
			cc.activeMu.Unlock()
		}

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
	if cc.connector == nil {
		return "codex"
	}
	return resolveCodexCommandFromConfig(cc.connector.Config.Codex)
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

func (cc *CodexClient) bootstrap(ctx context.Context) {
	syncSucceeded := true
	if err := cc.ensureWelcomeCodexChat(cc.backgroundContext(ctx)); err != nil {
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

func (cc *CodexClient) composeCodexChatInfo(portal *bridgev2.Portal, portalState *codexPortalState, canBackfill bool) *bridgev2.ChatInfo {
	title := "Codex"
	topic := ""
	if portalState != nil {
		if v := strings.TrimSpace(portalState.Title); v != "" {
			title = v
		}
		topic = cc.codexTopicForPortal(portal, portalState)
	}
	return bridgeutil.BuildLoginDMChatInfo(bridgeutil.LoginDMChatInfoParams{
		Title:          title,
		Topic:          topic,
		Login:          cc.UserLogin,
		HumanUserID:    humanUserID(cc.UserLogin.ID),
		BotUserID:      codexGhostID,
		BotDisplayName: "Codex",
		CanBackfill:    canBackfill,
	})
}

func (cc *CodexClient) buildSandboxPolicy(cwd string) map[string]any {
	return map[string]any{
		"type":                "workspaceWrite",
		"writableRoots":       []string{cwd},
		"networkAccess":       cc.codexNetworkAccess(),
		"excludeTmpdirEnvVar": false,
		"excludeSlashTmp":     false,
	}
}

func newRecoveredStreamingState(turnID, model string) *streamingState {
	return &streamingState{
		turnID:                 strings.TrimSpace(turnID),
		currentModel:           strings.TrimSpace(model),
		startedAtMs:            time.Now().UnixMilli(),
		firstToken:             true,
		codexTimelineNotices:   make(map[string]bool),
		codexToolOutputBuffers: make(map[string]*strings.Builder),
	}
}

func (cc *CodexClient) restoreRecoveredActiveTurns(portal *bridgev2.Portal, portalState *codexPortalState, thread codexThread, model string) {
	if cc == nil || portal == nil || portalState == nil {
		return
	}
	threadID := strings.TrimSpace(thread.ID)
	if threadID == "" {
		return
	}
	cc.activeMu.Lock()
	defer cc.activeMu.Unlock()
	for _, turn := range thread.Turns {
		if !strings.EqualFold(strings.TrimSpace(turn.Status), "inProgress") {
			continue
		}
		turnID := strings.TrimSpace(turn.ID)
		if turnID == "" {
			continue
		}
		key := codexTurnKey(threadID, turnID)
		if _, exists := cc.activeTurns[key]; exists {
			continue
		}
		cc.activeTurns[key] = &codexActiveTurn{
			portal:      portal,
			portalState: portalState,
			streamState: newRecoveredStreamingState(turnID, model),
			threadID:    threadID,
			turnID:      turnID,
			model:       strings.TrimSpace(model),
		}
	}
}

func (cc *CodexClient) ensureCodexThread(ctx context.Context, portal *bridgev2.Portal, portalState *codexPortalState) error {
	if portalState == nil || portal == nil {
		return errors.New("missing portal/meta")
	}
	if strings.TrimSpace(portalState.CodexCwd) == "" {
		return errors.New("codex working directory not set")
	}
	if _, err := os.Stat(portalState.CodexCwd); err != nil {
		return fmt.Errorf("working directory %s no longer exists", portalState.CodexCwd)
	}
	if strings.TrimSpace(portalState.CodexThreadID) != "" {
		return cc.ensureCodexThreadLoaded(ctx, portal, portalState)
	}
	if err := cc.ensureRPC(ctx); err != nil {
		return err
	}
	model := cc.connector.Config.Codex.DefaultModel
	var resp struct {
		Thread codexThread `json:"thread"`
		Model  string      `json:"model"`
	}
	callCtx, cancelCall := context.WithTimeout(ctx, 60*time.Second)
	defer cancelCall()
	err := cc.rpc.Call(callCtx, "thread/start", map[string]any{
		"model":                  model,
		"cwd":                    portalState.CodexCwd,
		"approvalPolicy":         "untrusted",
		"sandbox":                "workspace-write",
		"experimentalRawEvents":  false,
		"persistExtendedHistory": true,
	}, &resp)
	if err != nil {
		return err
	}
	portalState.CodexThreadID = strings.TrimSpace(resp.Thread.ID)
	if portalState.CodexThreadID == "" {
		return errors.New("codex returned empty thread id")
	}
	if err := saveCodexPortalState(ctx, portal, portalState); err != nil {
		return err
	}
	cc.loadedMu.Lock()
	cc.loadedThreads[portalState.CodexThreadID] = true
	cc.loadedMu.Unlock()
	cc.restoreRecoveredActiveTurns(portal, portalState, resp.Thread, resp.Model)
	if portal != nil && portal.MXID != "" {
		if info := cc.composeCodexChatInfo(portal, portalState, strings.TrimSpace(portalState.CodexThreadID) != ""); info != nil {
			portal.UpdateInfo(ctx, info, cc.UserLogin, nil, time.Time{})
		}
	}
	return nil
}

func (cc *CodexClient) ensureCodexThreadLoaded(ctx context.Context, portal *bridgev2.Portal, portalState *codexPortalState) error {
	if portalState == nil {
		return errors.New("missing metadata")
	}
	threadID := strings.TrimSpace(portalState.CodexThreadID)
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
		Thread codexThread `json:"thread"`
		Model  string      `json:"model"`
	}
	callCtx, cancelCall := context.WithTimeout(ctx, 60*time.Second)
	defer cancelCall()
	err := cc.rpc.Call(callCtx, "thread/resume", map[string]any{
		"threadId":               threadID,
		"model":                  cc.connector.Config.Codex.DefaultModel,
		"cwd":                    portalState.CodexCwd,
		"approvalPolicy":         "untrusted",
		"sandbox":                "workspace-write",
		"persistExtendedHistory": true,
	}, &resp)
	if err != nil {
		return err
	}
	cc.loadedMu.Lock()
	cc.loadedThreads[threadID] = true
	cc.loadedMu.Unlock()
	cc.restoreRecoveredActiveTurns(portal, portalState, resp.Thread, resp.Model)
	if portal != nil && portal.MXID != "" {
		if info := cc.composeCodexChatInfo(portal, portalState, strings.TrimSpace(portalState.CodexThreadID) != ""); info != nil {
			portal.UpdateInfo(ctx, info, cc.UserLogin, nil, time.Time{})
		}
	}
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
	state, err := loadCodexPortalState(ctx, msg.Portal)
	if err != nil {
		return err
	}
	if state.AwaitingCwdSetup {
		go func() {
			time.Sleep(1 * time.Second)
			_ = cc.ensureWelcomeCodexChat(cc.backgroundContext(ctx))
		}()
		return nil
	}
	if err := cc.ensureRPC(ctx); err != nil {
		return nil
	}

	// If a turn is in-flight for this thread, try to interrupt it.
	tid := strings.TrimSpace(state.CodexThreadID)
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
	if cwd := strings.TrimSpace(state.CodexCwd); cwd != "" {
		_ = os.RemoveAll(cwd)
	}
	state.CodexThreadID = ""
	state.CodexCwd = ""
	_ = saveCodexPortalState(ctx, msg.Portal, state)
	return nil
}

func (cc *CodexClient) sendSystemNotice(ctx context.Context, portal *bridgev2.Portal, message string) {
	if cc == nil || portal == nil || strings.TrimSpace(message) == "" {
		return
	}
	send := func(sendCtx context.Context) error {
		return sdk.SendSystemMessage(sendCtx, cc.UserLogin, portal, cc.senderForPortal(), message)
	}
	if portal.MXID == "" {
		go func() {
			retryCtx := cc.backgroundContext(ctx)
			for attempt := 0; attempt < 3; attempt++ {
				if portal.MXID != "" {
					if err := send(retryCtx); err != nil {
						cc.log.Warn().Err(err).Msg("Failed to send system notice")
					}
					return
				}
				time.Sleep(250 * time.Millisecond)
			}
			if portal.MXID == "" {
				cc.log.Warn().Msg("Portal MXID never became available, dropping system notice")
				return
			}
			if err := send(retryCtx); err != nil {
				cc.log.Warn().Err(err).Msg("Failed to send system notice")
			}
		}()
		return
	}
	if err := send(ctx); err != nil {
		cc.log.Warn().Err(err).Msg("Failed to send system notice")
	}
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
	state, err := loadCodexPortalState(ctx, pm.portal)
	if err != nil || state == nil {
		// Bad portal — discard.
		cc.popPendingCodex(roomID)
		cc.releaseRoom(roomID)
		cc.processPendingCodex(roomID)
		return
	}
	if err := cc.ensureCodexThreadLoaded(ctx, pm.portal, state); err != nil {
		cc.log.Warn().Err(err).Stringer("room", roomID).Msg("Pending codex message: thread load failed")
		cc.releaseRoom(roomID)
		return
	}
	// Committed — now pop.
	cc.popPendingCodex(roomID)
	go func() {
		func() {
			defer cc.releaseRoom(roomID)
			cc.runTurn(ctx, pm.portal, state, pm.event, pm.body)
		}()
		cc.processPendingCodex(roomID)
	}()
}

// Streaming helpers (Codex -> Matrix AI SDK chunk mapping)

func (cc *CodexClient) buildUIMessageMetadata(state *streamingState, model string, includeUsage bool, finishReason string) map[string]any {
	if state != nil && strings.TrimSpace(state.currentModel) != "" {
		model = state.currentModel
	}
	return sdk.BuildUIMessageMetadata(sdk.UIMessageMetadataParams{
		TurnID:           state.currentTurnID(),
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

func buildMessageMetadata(state *streamingState, turnID string, model string, finishReason string, uiMessage map[string]any) *MessageMetadata {
	if state != nil && strings.TrimSpace(state.currentModel) != "" {
		model = state.currentModel
	}
	snapshot := sdk.BuildTurnSnapshot(uiMessage, sdk.TurnDataBuildOptions{
		ID:             turnID,
		Role:           "assistant",
		Text:           state.accumulated.String(),
		Reasoning:      state.reasoning.String(),
		ToolCalls:      state.toolCalls,
		GeneratedFiles: sdk.GeneratedFileRefsFromParts(state.generatedFiles),
	}, "codex")
	bundle := sdk.BuildAssistantMetadataBundle(sdk.AssistantMetadataBundleParams{
		Snapshot:           snapshot,
		FinishReason:       finishReason,
		TurnID:             turnID,
		AgentID:            state.agentID,
		StartedAtMs:        state.startedAtMs,
		CompletedAtMs:      state.completedAtMs,
		PromptTokens:       state.promptTokens,
		CompletionTokens:   state.completionTokens,
		ReasoningTokens:    state.reasoningTokens,
		Model:              model,
		FirstTokenAtMs:     state.firstTokenAtMs,
		ThinkingTokenCount: len(strings.Fields(state.reasoning.String())),
	})
	return &MessageMetadata{
		BaseMessageMetadata:      bundle.Base,
		AssistantMessageMetadata: bundle.Assistant,
	}
}

func (cc *CodexClient) buildSDKFinalMetadata(turn *sdk.Turn, state *streamingState, model string, finishReason string) any {
	if turn == nil || state == nil {
		return &MessageMetadata{}
	}
	return buildMessageMetadata(state, turn.ID(), model, finishReason, streamui.SnapshotUIMessage(turn.UIState()))
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
	if state.turn == nil || state.turn.UIState() == nil {
		return
	}
	uiState := state.turn.UIState()
	uiState.InitMaps()
	uiState.UIToolCallIDByApproval[approvalID] = toolCallID
	uiState.UIToolApprovalRequested[approvalID] = true
	uiState.UIToolNameByToolCallID[toolCallID] = toolName
	uiState.UIToolTypeByToolCallID[toolCallID] = matrixevents.ToolTypeProvider
}
