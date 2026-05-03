package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/bridges/codex/codexrpc"
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

var aiBaseCaps = &event.RoomFeatures{
	ID:                  aiCapabilityID,
	MaxTextLength:       100000,
	Reply:               event.CapLevelFullySupported,
	Thread:              event.CapLevelFullySupported,
	Edit:                event.CapLevelFullySupported,
	Reaction:            event.CapLevelFullySupported,
	ReadReceipts:        true,
	TypingNotifications: true,
	DeleteChat:          true,
}

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

	roomMu      sync.Mutex
	activeRooms map[id.RoomID]bool
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
		UserLogin:     login,
		connector:     connector,
		log:           log,
		notifCh:       make(chan codexNotif, 4096),
		notifDone:     make(chan struct{}),
		loadedThreads: make(map[string]bool),
		activeTurns:   make(map[string]*codexActiveTurn),
		turnSubs:      make(map[string]chan codexNotif),
		activeRooms:   make(map[id.RoomID]bool),
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
