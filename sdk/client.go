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

	"github.com/beeper/agentremote"
)

// Compile-time interface checks.
var (
	_ bridgev2.NetworkAPI                    = (*sdkClient)(nil)
	_ bridgev2.EditHandlingNetworkAPI        = (*sdkClient)(nil)
	_ bridgev2.ReactionHandlingNetworkAPI    = (*sdkClient)(nil)
	_ bridgev2.RedactionHandlingNetworkAPI   = (*sdkClient)(nil)
	_ bridgev2.TypingHandlingNetworkAPI      = (*sdkClient)(nil)
	_ bridgev2.RoomNameHandlingNetworkAPI    = (*sdkClient)(nil)
	_ bridgev2.RoomTopicHandlingNetworkAPI   = (*sdkClient)(nil)
	_ bridgev2.BackfillingNetworkAPI         = (*sdkClient)(nil)
	_ bridgev2.DeleteChatHandlingNetworkAPI  = (*sdkClient)(nil)
	_ bridgev2.IdentifierResolvingNetworkAPI = (*sdkClient)(nil)
	_ bridgev2.ContactListingNetworkAPI      = (*sdkClient)(nil)
	_ bridgev2.UserSearchingNetworkAPI       = (*sdkClient)(nil)
)

// pendingSDKApprovalData holds SDK-specific metadata for a pending tool approval.
type pendingSDKApprovalData struct {
	RoomID     id.RoomID
	TurnID     string
	ToolCallID string
	ToolName   string
}

type sdkClient struct {
	agentremote.ClientBase
	cfg               *Config
	userLogin         *bridgev2.UserLogin
	approvalFlow      *agentremote.ApprovalFlow[*pendingSDKApprovalData]
	turnManager       *TurnManager
	conversationState *conversationStateStore

	sessionMu sync.RWMutex
	session   any
}

func newSDKClient(login *bridgev2.UserLogin, cfg *Config) *sdkClient {
	identity := resolveProviderIdentity(cfg)
	c := &sdkClient{
		cfg:               cfg,
		userLogin:         login,
		conversationState: newConversationStateStore(),
	}
	c.InitClientBase(login, c)
	c.approvalFlow = agentremote.NewApprovalFlow(agentremote.ApprovalFlowConfig[*pendingSDKApprovalData]{
		Login: func() *bridgev2.UserLogin { return c.userLogin },
		Sender: func(portal *bridgev2.Portal) bridgev2.EventSender {
			if cfg != nil && cfg.Agent != nil {
				return cfg.Agent.EventSender(login.ID)
			}
			return bridgev2.EventSender{}
		},
		IDPrefix: identity.IDPrefix,
		LogKey:   identity.LogKey,
		RoomIDFromData: func(data *pendingSDKApprovalData) id.RoomID {
			if data == nil {
				return ""
			}
			return data.RoomID
		},
		SendNotice: func(ctx context.Context, portal *bridgev2.Portal, msg string) {
			// Best-effort notice via bot intent.
			if login.Bridge != nil && login.Bridge.Bot != nil && portal != nil && portal.MXID != "" {
				_, _ = login.Bridge.Bot.SendMessage(ctx, portal.MXID, event.EventMessage, &event.Content{
					Parsed: &event.MessageEventContent{MsgType: event.MsgNotice, Body: msg},
				}, nil)
			}
		},
	})
	if cfg != nil && cfg.TurnManagement != nil {
		c.turnManager = NewTurnManager(cfg.TurnManagement)
	}
	return c
}

func (c *sdkClient) GetApprovalHandler() agentremote.ApprovalReactionHandler {
	return c.approvalFlow
}

func (c *sdkClient) config() *Config { return c.cfg }

func (c *sdkClient) sessionValue() any { return c.getSession() }

func (c *sdkClient) conversationStore() *conversationStateStore { return c.conversationState }

func (c *sdkClient) approvalFlowValue() *agentremote.ApprovalFlow[*pendingSDKApprovalData] {
	return c.approvalFlow
}

func (c *sdkClient) providerIdentity() ProviderIdentity {
	return resolveProviderIdentity(c.cfg)
}

func (c *sdkClient) getSession() any {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()
	return c.session
}

func (c *sdkClient) setSession(s any) {
	c.sessionMu.Lock()
	c.session = s
	c.sessionMu.Unlock()
}

// Connect implements bridgev2.NetworkAPI.
func (c *sdkClient) Connect(ctx context.Context) {
	if c.config().OnConnect != nil {
		info := &LoginInfo{
			Login:  c.userLogin,
			UserID: string(c.userLogin.UserMXID),
		}
		session, err := c.config().OnConnect(ctx, info)
		if err != nil {
			c.userLogin.BridgeState.Send(status.BridgeState{
				StateEvent: status.StateUnknownError,
				Error:      status.BridgeStateErrorCode(err.Error()),
			})
			return
		}
		c.setSession(session)
	}
	c.SetLoggedIn(true)
	c.userLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected})
}

func (c *sdkClient) Disconnect() {
	c.SetLoggedIn(false)
	if c.approvalFlow != nil {
		c.approvalFlow.Close()
	}
	c.CloseAllSessions()
	if c.config().OnDisconnect != nil {
		c.config().OnDisconnect(c.getSession())
	}
	c.setSession(nil)
}

func (c *sdkClient) LogoutRemote(ctx context.Context) {
	c.Disconnect()
}

func (c *sdkClient) IsThisUser(_ context.Context, userID networkid.UserID) bool {
	if c.config().IsThisUser != nil {
		return c.config().IsThisUser(string(userID))
	}
	return false
}

func (c *sdkClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	if c.config().GetChatInfo != nil {
		return c.config().GetChatInfo(c.conv(ctx, portal))
	}
	return nil, nil
}

func (c *sdkClient) GetUserInfo(_ context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	if c.config().GetUserInfo != nil {
		return c.config().GetUserInfo(ghost)
	}
	return nil, nil
}

func (c *sdkClient) GetCapabilities(_ context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	conv := c.conv(context.Background(), portal)
	return convertRoomFeatures(conv.currentRoomFeatures(context.Background()))
}

func (c *sdkClient) conv(ctx context.Context, portal *bridgev2.Portal) *Conversation {
	return newConversation(ctx, portal, c.userLogin, bridgev2.EventSender{}, c)
}

// HandleMatrixMessage dispatches incoming messages to the OnMessage callback.
func (c *sdkClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	if c.config().OnMessage == nil {
		return nil, nil
	}
	runCtx := c.BackgroundContext(ctx)
	sdkMsg := convertMatrixMessage(msg)
	conv := c.conv(runCtx, msg.Portal)
	session := c.getSession()
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
		return c.config().OnMessage(session, conv, sdkMsg, turn)
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

func convertMatrixMessage(msg *bridgev2.MatrixMessage) *Message {
	content, ok := msg.Event.Content.Parsed.(*event.MessageEventContent)
	if !ok {
		return &Message{
			ID:        msg.Event.ID.String(),
			Timestamp: time.UnixMilli(msg.Event.Timestamp),
			RawEvent:  msg.Event,
			RawMsg:    msg,
		}
	}

	m := &Message{
		ID:        msg.Event.ID.String(),
		Text:      content.Body,
		HTML:      content.FormattedBody,
		Timestamp: time.UnixMilli(msg.Event.Timestamp),
		RawEvent:  msg.Event,
		RawMsg:    msg,
	}

	switch content.MsgType {
	case event.MsgImage:
		m.MsgType = MessageImage
	case event.MsgAudio:
		m.MsgType = MessageAudio
	case event.MsgVideo:
		m.MsgType = MessageVideo
	case event.MsgFile:
		m.MsgType = MessageFile
	default:
		m.MsgType = MessageText
	}

	if content.URL != "" {
		m.MediaURL = string(content.URL)
	}
	if content.Info != nil {
		m.MediaType = content.Info.MimeType
	}
	if content.RelatesTo != nil && content.RelatesTo.InReplyTo != nil {
		m.ReplyTo = content.RelatesTo.InReplyTo.EventID.String()
	}

	return m
}

// HandleMatrixEdit implements bridgev2.EditHandlingNetworkAPI.
func (c *sdkClient) HandleMatrixEdit(ctx context.Context, edit *bridgev2.MatrixEdit) error {
	if c.config().OnEdit == nil {
		return nil
	}
	me := &MessageEdit{
		OriginalID: string(edit.EditTarget.ID),
		RawEdit:    edit,
	}
	if edit.Content != nil {
		me.NewText = edit.Content.Body
		me.NewHTML = edit.Content.FormattedBody
	}
	return c.config().OnEdit(c.getSession(), c.conv(ctx, edit.Portal), me)
}

// HandleMatrixMessageRemove implements bridgev2.RedactionHandlingNetworkAPI.
func (c *sdkClient) HandleMatrixMessageRemove(ctx context.Context, msg *bridgev2.MatrixMessageRemove) error {
	if c.config().OnDelete == nil {
		return nil
	}
	var msgID string
	if msg.TargetMessage != nil {
		msgID = string(msg.TargetMessage.ID)
	}
	return c.config().OnDelete(c.getSession(), c.conv(ctx, msg.Portal), msgID)
}

// HandleMatrixTyping implements bridgev2.TypingHandlingNetworkAPI.
func (c *sdkClient) HandleMatrixTyping(ctx context.Context, msg *bridgev2.MatrixTyping) error {
	if c.config().OnTyping != nil {
		c.config().OnTyping(c.getSession(), c.conv(ctx, msg.Portal), msg.IsTyping)
	}
	return nil
}

// HandleMatrixRoomName implements bridgev2.RoomNameHandlingNetworkAPI.
func (c *sdkClient) HandleMatrixRoomName(ctx context.Context, msg *bridgev2.MatrixRoomName) (bool, error) {
	if c.config().OnRoomName != nil {
		return c.config().OnRoomName(c.getSession(), c.conv(ctx, msg.Portal), msg.Content.Name)
	}
	return false, nil
}

// HandleMatrixRoomTopic implements bridgev2.RoomTopicHandlingNetworkAPI.
func (c *sdkClient) HandleMatrixRoomTopic(ctx context.Context, msg *bridgev2.MatrixRoomTopic) (bool, error) {
	if c.config().OnRoomTopic != nil {
		return c.config().OnRoomTopic(c.getSession(), c.conv(ctx, msg.Portal), msg.Content.Topic)
	}
	return false, nil
}

// FetchMessages implements bridgev2.BackfillingNetworkAPI.
func (c *sdkClient) FetchMessages(ctx context.Context, params bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	if c.config().FetchMessages == nil {
		return nil, nil
	}
	return c.config().FetchMessages(ctx, params)
}

// HandleMatrixDeleteChat implements bridgev2.DeleteChatHandlingNetworkAPI.
func (c *sdkClient) HandleMatrixDeleteChat(ctx context.Context, msg *bridgev2.MatrixDeleteChat) error {
	if c.config().DeleteChat == nil {
		return nil
	}
	return c.config().DeleteChat(c.conv(ctx, msg.Portal))
}

// ResolveIdentifier implements bridgev2.IdentifierResolvingNetworkAPI.
func (c *sdkClient) ResolveIdentifier(ctx context.Context, identifier string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	if c.config().ResolveIdentifier == nil {
		return nil, nil
	}
	return c.config().ResolveIdentifier(ctx, c.getSession(), identifier, createChat)
}

// GetContactList implements bridgev2.ContactListingNetworkAPI.
func (c *sdkClient) GetContactList(ctx context.Context) ([]*bridgev2.ResolveIdentifierResponse, error) {
	if c.config().GetContactList == nil {
		return nil, nil
	}
	return c.config().GetContactList(ctx, c.getSession())
}

// SearchUsers implements bridgev2.UserSearchingNetworkAPI.
func (c *sdkClient) SearchUsers(ctx context.Context, query string) ([]*bridgev2.ResolveIdentifierResponse, error) {
	if c.config().SearchUsers == nil {
		return nil, nil
	}
	return c.config().SearchUsers(ctx, c.getSession(), query)
}
