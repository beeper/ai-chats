package sdk

import (
	"context"
	"fmt"
	"sync"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

var _ bridgev2.NetworkAPI = (*sdkClient[any, any])(nil)

// pendingSDKApprovalData holds SDK-specific metadata for a pending tool approval.
type pendingSDKApprovalData struct {
	RoomID     id.RoomID
	TurnID     string
	ToolCallID string
	ToolName   string
}

type sdkClient[SessionT SessionValue, ConfigDataT ConfigValue] struct {
	ClientBase
	cfg               *Config[SessionT, ConfigDataT]
	userLogin         *bridgev2.UserLogin
	approvalFlow      *ApprovalFlow[*pendingSDKApprovalData]
	turnManager       *TurnManager
	conversationState *conversationStateStore

	sessionMu sync.RWMutex
	session   SessionT
}

func newSDKClient[SessionT SessionValue, ConfigDataT ConfigValue](login *bridgev2.UserLogin, cfg *Config[SessionT, ConfigDataT]) *sdkClient[SessionT, ConfigDataT] {
	identity := normalizedProviderIdentity(ProviderIdentity{})
	if cfg != nil {
		identity = normalizedProviderIdentity(cfg.ProviderIdentity)
	}
	senderForPortal := func(*bridgev2.Portal) bridgev2.EventSender {
		if cfg != nil && cfg.Agent != nil {
			return cfg.Agent.EventSender(login.ID)
		}
		return bridgev2.EventSender{}
	}
	c := &sdkClient[SessionT, ConfigDataT]{
		cfg:               cfg,
		userLogin:         login,
		conversationState: newConversationStateStore(),
	}
	c.InitClientBase(login, c)
	c.approvalFlow = NewApprovalFlow(ApprovalFlowConfig[*pendingSDKApprovalData]{
		Login:    func() *bridgev2.UserLogin { return c.userLogin },
		Sender:   senderForPortal,
		IDPrefix: identity.IDPrefix,
		LogKey:   identity.LogKey,
		RoomIDFromData: func(data *pendingSDKApprovalData) id.RoomID {
			if data == nil {
				return ""
			}
			return data.RoomID
		},
		SendNotice: func(ctx context.Context, portal *bridgev2.Portal, msg string) {
			_ = SendSystemMessage(ctx, login, portal, senderForPortal(portal), msg)
		},
	})
	if cfg != nil && cfg.TurnManagement != nil {
		c.turnManager = NewTurnManager(cfg.TurnManagement)
	}
	return c
}

func (c *sdkClient[SessionT, ConfigDataT]) GetApprovalHandler() ApprovalReactionHandler {
	return c.approvalFlow
}

// Connect implements bridgev2.NetworkAPI.
func (c *sdkClient[SessionT, ConfigDataT]) Connect(ctx context.Context) {
	if c.cfg != nil && c.cfg.OnConnect != nil {
		info := &LoginInfo{
			Login:  c.userLogin,
			UserID: string(c.userLogin.UserMXID),
		}
		session, err := c.cfg.OnConnect(ctx, info)
		if err != nil {
			c.userLogin.BridgeState.Send(status.BridgeState{
				StateEvent: status.StateUnknownError,
				Error:      status.BridgeStateErrorCode(err.Error()),
			})
			return
		}
		c.sessionMu.Lock()
		c.session = session
		c.sessionMu.Unlock()
	}
	c.SetLoggedIn(true)
	c.userLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected})
}

func (c *sdkClient[SessionT, ConfigDataT]) Disconnect() {
	c.SetLoggedIn(false)
	if c.approvalFlow != nil {
		c.approvalFlow.Close()
	}
	c.CloseAllSessions()
	if c.cfg != nil && c.cfg.OnDisconnect != nil {
		c.sessionMu.RLock()
		session := c.session
		c.sessionMu.RUnlock()
		c.cfg.OnDisconnect(session)
	}
	var zero SessionT
	c.sessionMu.Lock()
	c.session = zero
	c.sessionMu.Unlock()
}

func (c *sdkClient[SessionT, ConfigDataT]) LogoutRemote(ctx context.Context) {
	c.Disconnect()
}

func (c *sdkClient[SessionT, ConfigDataT]) IsThisUser(_ context.Context, userID networkid.UserID) bool {
	if c.cfg != nil && c.cfg.IsThisUser != nil {
		return c.cfg.IsThisUser(string(userID))
	}
	return false
}

func (c *sdkClient[SessionT, ConfigDataT]) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	if c.cfg != nil && c.cfg.GetChatInfo != nil {
		c.sessionMu.RLock()
		session := c.session
		c.sessionMu.RUnlock()
		return c.cfg.GetChatInfo(NewConversation(ctx, c.userLogin, portal, bridgev2.EventSender{}, c.cfg, session, NewConversationOptions{
			ApprovalFlow: c.approvalFlow,
			StateStore:   c.conversationState,
		}))
	}
	return nil, nil
}

func (c *sdkClient[SessionT, ConfigDataT]) GetUserInfo(_ context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	if c.cfg != nil && c.cfg.GetUserInfo != nil {
		return c.cfg.GetUserInfo(ghost)
	}
	return nil, nil
}

func (c *sdkClient[SessionT, ConfigDataT]) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	c.sessionMu.RLock()
	session := c.session
	c.sessionMu.RUnlock()
	conv := NewConversation(ctx, c.userLogin, portal, bridgev2.EventSender{}, c.cfg, session, NewConversationOptions{
		ApprovalFlow: c.approvalFlow,
		StateStore:   c.conversationState,
	})
	features := conv.currentRoomFeatures(ctx)
	if features == nil {
		features = defaultSDKFeatureConfig()
	}
	maxText := features.MaxTextLength
	if maxText == 0 {
		maxText = DefaultAgentMaxTextLength
	}
	capID := features.CustomCapabilityID
	if capID == "" {
		capID = "com.beeper.agentremote.sdk"
	}
	roomFeatures := &event.RoomFeatures{
		ID:                  capID,
		MaxTextLength:       maxText,
		Reply:               capLevel(features.SupportsReply),
		Edit:                capLevel(features.SupportsEdit),
		Delete:              capLevel(features.SupportsDelete),
		Reaction:            capLevel(features.SupportsReactions),
		ReadReceipts:        features.SupportsReadReceipts,
		TypingNotifications: features.SupportsTyping,
		DeleteChat:          features.SupportsDeleteChat,
		File:                make(event.FileFeatureMap),
	}
	if features.SupportsImages {
		roomFeatures.File[event.MsgImage] = &event.FileFeatures{}
	}
	if features.SupportsAudio {
		roomFeatures.File[event.MsgAudio] = &event.FileFeatures{}
	}
	if features.SupportsVideo {
		roomFeatures.File[event.MsgVideo] = &event.FileFeatures{}
	}
	if features.SupportsFiles {
		roomFeatures.File[event.MsgFile] = &event.FileFeatures{}
	}
	return roomFeatures
}

// HandleMatrixMessage dispatches incoming messages to the OnMessage callback.
func (c *sdkClient[SessionT, ConfigDataT]) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	if c.cfg == nil || c.cfg.OnMessage == nil {
		return nil, nil
	}
	runCtx := ctx
	if runCtx == nil {
		if c.userLogin != nil && c.userLogin.Bridge != nil && c.userLogin.Bridge.BackgroundCtx != nil {
			runCtx = c.userLogin.Bridge.BackgroundCtx
		} else {
			runCtx = context.Background()
		}
	}
	content, ok := msg.Event.Content.Parsed.(*event.MessageEventContent)
	sdkMsg := &Message{
		ID:        msg.Event.ID.String(),
		Timestamp: time.UnixMilli(msg.Event.Timestamp),
	}
	if ok {
		sdkMsg.Text = content.Body
		sdkMsg.HTML = content.FormattedBody
		switch content.MsgType {
		case event.MsgImage:
			sdkMsg.MsgType = MessageImage
		case event.MsgAudio:
			sdkMsg.MsgType = MessageAudio
		case event.MsgVideo:
			sdkMsg.MsgType = MessageVideo
		case event.MsgFile:
			sdkMsg.MsgType = MessageFile
		default:
			sdkMsg.MsgType = MessageText
		}
		if content.URL != "" {
			sdkMsg.MediaURL = string(content.URL)
		}
		if content.Info != nil {
			sdkMsg.MediaType = content.Info.MimeType
		}
		if content.RelatesTo != nil && content.RelatesTo.InReplyTo != nil {
			sdkMsg.ReplyTo = content.RelatesTo.InReplyTo.EventID.String()
		}
	}
	c.sessionMu.RLock()
	session := c.session
	c.sessionMu.RUnlock()
	conv := NewConversation(runCtx, c.userLogin, msg.Portal, bridgev2.EventSender{}, c.cfg, session, NewConversationOptions{
		ApprovalFlow: c.approvalFlow,
		StateStore:   c.conversationState,
	})
	var source *SourceRef
	if msg.Event != nil {
		source = UserMessageSource(msg.Event.ID.String())
	}
	agent, _ := conv.resolveDefaultAgent(runCtx)
	turn := conv.StartTurn(runCtx, agent, source)
	roomID := string(msg.Portal.ID)
	if c.turnManager != nil {
		roomID = c.turnManager.ResolveKey(roomID)
	}
	run := func(turnCtx context.Context) error {
		return c.cfg.OnMessage(session, conv, sdkMsg, turn)
	}
	go func() {
		var err error
		if c.turnManager == nil {
			err = run(runCtx)
		} else {
			err = c.turnManager.Run(runCtx, roomID, run)
		}
		if err == nil {
			return
		}
		c.userLogin.Log.Error().
			Err(err).
			Str("portal_id", roomID).
			Str("login_id", string(c.userLogin.ID)).
			Msg("SDK matrix message handler failed")
		turn.EndWithError(fmt.Sprintf("Request failed: %v", err))
	}()
	return &bridgev2.MatrixMessageResponse{Pending: true}, nil
}
