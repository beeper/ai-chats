package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/bridges/codex/codexrpc"
	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

var (
	_ bridgev2.NetworkAPI                  = (*CodexClient)(nil)
	_ bridgev2.DeleteChatHandlingNetworkAPI = (*CodexClient)(nil)
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

// CodexClient implements bridgev2.NetworkAPI for the Codex bridge.
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
	activeTurns map[string]*codexActiveTurn

	subMu        sync.Mutex
	turnSubs     map[string]chan codexNotif
	dispatchOnce sync.Once

	loadedMu      sync.Mutex
	loadedThreads map[string]bool

	approvals *bridgeadapter.ApprovalManager[ToolApprovalDecisionCodex]

	bootstrapOnce sync.Once

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
		return nil, errors.New("missing connector")
	}
	meta := loginMetadata(login)
	if !strings.EqualFold(strings.TrimSpace(meta.Provider), ProviderCodex) {
		return nil, fmt.Errorf("invalid provider: %s", meta.Provider)
	}
	return &CodexClient{
		UserLogin:       login,
		connector:       connector,
		log:             login.Log.With().Str("component", "codex").Logger(),
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

func (cc *CodexClient) backgroundContext(ctx context.Context) context.Context {
	base := context.Background()
	if cc.UserLogin != nil && cc.UserLogin.Bridge != nil && cc.UserLogin.Bridge.BackgroundCtx != nil {
		base = cc.UserLogin.Bridge.BackgroundCtx
	}
	return cc.loggerForContext(ctx).WithContext(base)
}

// --- Lifecycle ---

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
	}
	_ = cc.rpc.Call(readCtx, "account/read", map[string]any{"refreshToken": false}, &resp)
	if resp.Account != nil {
		cc.loggedIn.Store(true)
		meta := loginMetadata(cc.UserLogin)
		if email := strings.TrimSpace(resp.Account.Email); email != "" {
			meta.CodexAccountEmail = email
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

	if cc.notifDone != nil {
		select {
		case <-cc.notifDone:
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
	if err := cc.ensureRPC(cc.backgroundContext(ctx)); err == nil && cc.rpc != nil {
		callCtx, cancel := context.WithTimeout(cc.backgroundContext(ctx), 10*time.Second)
		defer cancel()
		var out map[string]any
		_ = cc.rpc.Call(callCtx, "account/logout", nil, &out)
	}
	cc.purgeCodexHomeBestEffort(ctx)
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

// --- Cleanup ---

func (cc *CodexClient) purgeCodexHomeBestEffort(ctx context.Context) {
	if cc.UserLogin == nil {
		return
	}
	meta, ok := cc.UserLogin.Metadata.(*UserLoginMetadata)
	if !ok || meta == nil || !meta.CodexHomeManaged {
		return
	}
	codexHome := strings.TrimSpace(meta.CodexHome)
	if codexHome == "" {
		return
	}
	clean := filepath.Clean(codexHome)
	if clean == string(os.PathSeparator) || clean == "." {
		return
	}
	_ = os.RemoveAll(clean)
}

func (cc *CodexClient) purgeCodexCwdsBestEffort(ctx context.Context) {
	if cc.UserLogin == nil || cc.UserLogin.Bridge == nil || cc.UserLogin.Bridge.DB == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ups, err := cc.UserLogin.Bridge.DB.UserPortal.GetAllForLogin(ctx, cc.UserLogin.UserLogin)
	if err != nil || len(ups) == 0 {
		return
	}

	tmp := filepath.Clean(os.TempDir())
	if tmp == "" || tmp == "." || tmp == string(os.PathSeparator) {
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

// --- Query methods ---

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
	if ghost != nil && ghost.ID == codexGhostID {
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

// --- Message handling ---

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

	if meta.AwaitingCwdSetup {
		return cc.handleCwdSetup(ctx, portal, meta, msg, body)
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

	userMsg := cc.buildUserMessage(portal, msg, body)
	if _, err := cc.UserLogin.Bridge.GetGhostByID(ctx, userMsg.SenderID); err != nil {
		cc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to ensure user ghost before saving message")
	}
	if err := cc.UserLogin.Bridge.DB.Message.Insert(ctx, userMsg); err != nil {
		cc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to insert user message")
	}

	if !cc.acquireRoom(roomID) {
		cc.sendPendingStatus(ctx, portal, msg.Event, "Queued -- waiting for current turn to finish...")
		cc.queuePendingCodex(roomID, &codexPendingMessage{
			event:  msg.Event,
			portal: portal,
			meta:   meta,
			body:   body,
		})
		return &bridgev2.MatrixMessageResponse{DB: userMsg, Pending: true}, nil
	}

	cc.sendPendingStatus(ctx, portal, msg.Event, "Processing...")
	go func() {
		func() {
			defer cc.releaseRoom(roomID)
			cc.runTurn(cc.backgroundContext(ctx), portal, meta, msg.Event, body)
		}()
		cc.processPendingCodex(roomID)
	}()

	return &bridgev2.MatrixMessageResponse{DB: userMsg, Pending: true}, nil
}

func (cc *CodexClient) handleCwdSetup(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, msg *bridgev2.MatrixMessage, body string) (*bridgev2.MatrixMessageResponse, error) {
	path := strings.TrimSpace(msg.Content.Body)
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		cc.sendSystemNotice(ctx, portal, "That path doesn't exist or isn't a directory. Send a valid absolute path.")
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

func (cc *CodexClient) buildUserMessage(portal *bridgev2.Portal, msg *bridgev2.MatrixMessage, body string) *database.Message {
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
	return userMsg
}
