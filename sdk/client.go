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
	_ bridgev2.NetworkAPI                    = (*sdkClient[any, any])(nil)
	_ bridgev2.EditHandlingNetworkAPI        = (*sdkClient[any, any])(nil)
	_ bridgev2.ReactionHandlingNetworkAPI    = (*sdkClient[any, any])(nil)
	_ bridgev2.RedactionHandlingNetworkAPI   = (*sdkClient[any, any])(nil)
	_ bridgev2.TypingHandlingNetworkAPI      = (*sdkClient[any, any])(nil)
	_ bridgev2.RoomNameHandlingNetworkAPI    = (*sdkClient[any, any])(nil)
	_ bridgev2.RoomTopicHandlingNetworkAPI   = (*sdkClient[any, any])(nil)
	_ bridgev2.BackfillingNetworkAPI         = (*sdkClient[any, any])(nil)
	_ bridgev2.DeleteChatHandlingNetworkAPI  = (*sdkClient[any, any])(nil)
	_ bridgev2.IdentifierResolvingNetworkAPI = (*sdkClient[any, any])(nil)
	_ bridgev2.ContactListingNetworkAPI      = (*sdkClient[any, any])(nil)
	_ bridgev2.UserSearchingNetworkAPI       = (*sdkClient[any, any])(nil)
)

// pendingSDKApprovalData holds SDK-specific metadata for a pending tool approval.
type pendingSDKApprovalData struct {
	RoomID     id.RoomID
	TurnID     string
	ToolCallID string
	ToolName   string
}

type sdkClient[SessionT SessionValue, ConfigDataT ConfigValue] struct {
	agentremote.ClientBase
	cfg               *Config[SessionT, ConfigDataT]
	userLogin         *bridgev2.UserLogin
	approvalFlow      *agentremote.ApprovalFlow[*pendingSDKApprovalData]
	turnManager       *TurnManager
	conversationState *conversationStateStore

	sessionMu sync.RWMutex
	session   SessionT
}

func newSDKClient[SessionT SessionValue, ConfigDataT ConfigValue](login *bridgev2.UserLogin, cfg *Config[SessionT, ConfigDataT]) *sdkClient[SessionT, ConfigDataT] {
	identity := resolveProviderIdentity(cfg)
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
	c.approvalFlow = agentremote.NewApprovalFlow(agentremote.ApprovalFlowConfig[*pendingSDKApprovalData]{
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
			_ = agentremote.SendSystemMessage(ctx, login, portal, senderForPortal(portal), msg)
		},
	})
	if cfg != nil && cfg.TurnManagement != nil {
		c.turnManager = NewTurnManager(cfg.TurnManagement)
	}
	return c
}

func (c *sdkClient[SessionT, ConfigDataT]) GetApprovalHandler() agentremote.ApprovalReactionHandler {
	return c.approvalFlow
}

func (c *sdkClient[SessionT, ConfigDataT]) agent() *Agent {
	if c == nil || c.cfg == nil {
		return nil
	}
	return c.cfg.Agent
}

func (c *sdkClient[SessionT, ConfigDataT]) agentCatalog() AgentCatalog {
	if c == nil || c.cfg == nil {
		return nil
	}
	return c.cfg.AgentCatalog
}

func (c *sdkClient[SessionT, ConfigDataT]) roomFeatures(conv *Conversation) *RoomFeatures {
	if c == nil || c.cfg == nil {
		return nil
	}
	if c.cfg.GetCapabilities != nil {
		if rf := c.cfg.GetCapabilities(c.getSession(), conv); rf != nil {
			return rf
		}
	}
	return c.cfg.RoomFeatures
}

func (c *sdkClient[SessionT, ConfigDataT]) commands() []Command {
	if c == nil || c.cfg == nil {
		return nil
	}
	return c.cfg.Commands
}

func (c *sdkClient[SessionT, ConfigDataT]) turnConfig() *TurnConfig {
	if c == nil || c.cfg == nil {
		return nil
	}
	return c.cfg.TurnManagement
}

func (c *sdkClient[SessionT, ConfigDataT]) conversationStore() *conversationStateStore {
	return c.conversationState
}

func (c *sdkClient[SessionT, ConfigDataT]) approvalFlowValue() *agentremote.ApprovalFlow[*pendingSDKApprovalData] {
	return c.approvalFlow
}

func (c *sdkClient[SessionT, ConfigDataT]) providerIdentity() ProviderIdentity {
	return resolveProviderIdentity(c.cfg)
}

func (c *sdkClient[SessionT, ConfigDataT]) getSession() SessionT {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()
	return c.session
}

func (c *sdkClient[SessionT, ConfigDataT]) setSession(s SessionT) {
	c.sessionMu.Lock()
	c.session = s
	c.sessionMu.Unlock()
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
		c.setSession(session)
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
		c.cfg.OnDisconnect(c.getSession())
	}
	var zero SessionT
	c.setSession(zero)
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
		return c.cfg.GetChatInfo(c.conv(ctx, portal))
	}
	return nil, nil
}

func (c *sdkClient[SessionT, ConfigDataT]) GetUserInfo(_ context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	if c.cfg != nil && c.cfg.GetUserInfo != nil {
		return c.cfg.GetUserInfo(ghost)
	}
	return nil, nil
}

func (c *sdkClient[SessionT, ConfigDataT]) GetCapabilities(_ context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	conv := c.conv(context.Background(), portal)
	return convertRoomFeatures(conv.currentRoomFeatures(context.Background()))
}

func (c *sdkClient[SessionT, ConfigDataT]) conv(ctx context.Context, portal *bridgev2.Portal) *Conversation {
	return newConversation(ctx, portal, c.userLogin, bridgev2.EventSender{}, c)
}

// HandleMatrixMessage dispatches incoming messages to the OnMessage callback.
func (c *sdkClient[SessionT, ConfigDataT]) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	if c.cfg == nil || c.cfg.OnMessage == nil {
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
func (c *sdkClient[SessionT, ConfigDataT]) HandleMatrixEdit(ctx context.Context, edit *bridgev2.MatrixEdit) error {
	if c.cfg == nil || c.cfg.OnEdit == nil {
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
	return c.cfg.OnEdit(c.getSession(), c.conv(ctx, edit.Portal), me)
}

// HandleMatrixMessageRemove implements bridgev2.RedactionHandlingNetworkAPI.
func (c *sdkClient[SessionT, ConfigDataT]) HandleMatrixMessageRemove(ctx context.Context, msg *bridgev2.MatrixMessageRemove) error {
	if c.cfg == nil || c.cfg.OnDelete == nil {
		return nil
	}
	var msgID string
	if msg.TargetMessage != nil {
		msgID = string(msg.TargetMessage.ID)
	}
	return c.cfg.OnDelete(c.getSession(), c.conv(ctx, msg.Portal), msgID)
}

// HandleMatrixTyping implements bridgev2.TypingHandlingNetworkAPI.
func (c *sdkClient[SessionT, ConfigDataT]) HandleMatrixTyping(ctx context.Context, msg *bridgev2.MatrixTyping) error {
	if c.cfg != nil && c.cfg.OnTyping != nil {
		c.cfg.OnTyping(c.getSession(), c.conv(ctx, msg.Portal), msg.IsTyping)
	}
	return nil
}

// HandleMatrixRoomName implements bridgev2.RoomNameHandlingNetworkAPI.
func (c *sdkClient[SessionT, ConfigDataT]) HandleMatrixRoomName(ctx context.Context, msg *bridgev2.MatrixRoomName) (bool, error) {
	if c.cfg != nil && c.cfg.OnRoomName != nil {
		return c.cfg.OnRoomName(c.getSession(), c.conv(ctx, msg.Portal), msg.Content.Name)
	}
	return false, nil
}

// HandleMatrixRoomTopic implements bridgev2.RoomTopicHandlingNetworkAPI.
func (c *sdkClient[SessionT, ConfigDataT]) HandleMatrixRoomTopic(ctx context.Context, msg *bridgev2.MatrixRoomTopic) (bool, error) {
	if c.cfg != nil && c.cfg.OnRoomTopic != nil {
		return c.cfg.OnRoomTopic(c.getSession(), c.conv(ctx, msg.Portal), msg.Content.Topic)
	}
	return false, nil
}

// FetchMessages implements bridgev2.BackfillingNetworkAPI.
func (c *sdkClient[SessionT, ConfigDataT]) FetchMessages(ctx context.Context, params bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	if c.cfg == nil || c.cfg.FetchMessages == nil {
		return nil, nil
	}
	return c.cfg.FetchMessages(ctx, params)
}

// HandleMatrixDeleteChat implements bridgev2.DeleteChatHandlingNetworkAPI.
func (c *sdkClient[SessionT, ConfigDataT]) HandleMatrixDeleteChat(ctx context.Context, msg *bridgev2.MatrixDeleteChat) error {
	if c.cfg == nil || c.cfg.DeleteChat == nil {
		return nil
	}
	return c.cfg.DeleteChat(c.conv(ctx, msg.Portal))
}

// ResolveIdentifier implements bridgev2.IdentifierResolvingNetworkAPI.
func (c *sdkClient[SessionT, ConfigDataT]) ResolveIdentifier(ctx context.Context, identifier string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	if c.cfg == nil || c.cfg.ResolveIdentifier == nil {
		return nil, nil
	}
	return c.cfg.ResolveIdentifier(ctx, c.getSession(), identifier, createChat)
}

// GetContactList implements bridgev2.ContactListingNetworkAPI.
func (c *sdkClient[SessionT, ConfigDataT]) GetContactList(ctx context.Context) ([]*bridgev2.ResolveIdentifierResponse, error) {
	if c.cfg == nil || c.cfg.GetContactList == nil {
		return nil, nil
	}
	return c.cfg.GetContactList(ctx, c.getSession())
}

// SearchUsers implements bridgev2.UserSearchingNetworkAPI.
func (c *sdkClient[SessionT, ConfigDataT]) SearchUsers(ctx context.Context, query string) ([]*bridgev2.ResolveIdentifierResponse, error) {
	if c.cfg == nil || c.cfg.SearchUsers == nil {
		return nil, nil
	}
	return c.cfg.SearchUsers(ctx, c.getSession(), query)
}
