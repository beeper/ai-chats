package sdk

import (
	"context"
	"sync/atomic"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote"
)

// Compile-time interface checks for BaseClient.
var (
	_ bridgev2.NetworkAPI                    = (*BaseClient)(nil)
	_ bridgev2.EditHandlingNetworkAPI        = (*BaseClient)(nil)
	_ bridgev2.ReactionHandlingNetworkAPI    = (*BaseClient)(nil)
	_ bridgev2.RedactionHandlingNetworkAPI   = (*BaseClient)(nil)
	_ bridgev2.TypingHandlingNetworkAPI      = (*BaseClient)(nil)
	_ bridgev2.RoomNameHandlingNetworkAPI    = (*BaseClient)(nil)
	_ bridgev2.RoomTopicHandlingNetworkAPI   = (*BaseClient)(nil)
	_ bridgev2.BackfillingNetworkAPI         = (*BaseClient)(nil)
	_ bridgev2.DeleteChatHandlingNetworkAPI  = (*BaseClient)(nil)
	_ bridgev2.IdentifierResolvingNetworkAPI = (*BaseClient)(nil)
)

// BaseClient provides default no-op implementations for all bridgev2 network
// interfaces. Complex bridges can embed this and override specific methods.
type BaseClient struct {
	agentremote.ClientBase
	UserLogin   *bridgev2.UserLogin
	ServiceName string
	IDPrefix    string
	LogKey      string
	loggedIn    atomic.Bool
}

// InitBaseClient initialises the BaseClient fields.
func (c *BaseClient) InitBaseClient(login *bridgev2.UserLogin) {
	c.UserLogin = login
	c.InitClientBase(login, c)
}

// Connect implements bridgev2.NetworkAPI.
func (c *BaseClient) Connect(ctx context.Context) {
	c.loggedIn.Store(true)
	if c.UserLogin != nil {
		c.UserLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected})
	}
}

// Disconnect implements bridgev2.NetworkAPI.
func (c *BaseClient) Disconnect() {
	c.loggedIn.Store(false)
	c.CloseAllSessions()
}

// IsLoggedIn implements bridgev2.NetworkAPI.
func (c *BaseClient) IsLoggedIn() bool {
	return c.loggedIn.Load()
}

// LogoutRemote implements bridgev2.NetworkAPI.
func (c *BaseClient) LogoutRemote(ctx context.Context) {
	c.Disconnect()
}

// IsThisUser implements bridgev2.NetworkAPI.
func (c *BaseClient) IsThisUser(_ context.Context, _ networkid.UserID) bool {
	return false
}

// GetChatInfo implements bridgev2.NetworkAPI.
func (c *BaseClient) GetChatInfo(_ context.Context, _ *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	return nil, nil
}

// GetUserInfo implements bridgev2.NetworkAPI.
func (c *BaseClient) GetUserInfo(_ context.Context, _ *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	return nil, nil
}

// GetCapabilities implements bridgev2.NetworkAPI.
func (c *BaseClient) GetCapabilities(_ context.Context, _ *bridgev2.Portal) *event.RoomFeatures {
	return defaultSDKRoomFeatures()
}

// HandleMatrixMessage implements bridgev2.NetworkAPI.
func (c *BaseClient) HandleMatrixMessage(_ context.Context, _ *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	return nil, nil
}

// HandleMatrixEdit implements bridgev2.EditHandlingNetworkAPI.
func (c *BaseClient) HandleMatrixEdit(_ context.Context, _ *bridgev2.MatrixEdit) error {
	return nil
}

// PreHandleMatrixReaction implements bridgev2.ReactionHandlingNetworkAPI.
func (c *BaseClient) PreHandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (bridgev2.MatrixReactionPreResponse, error) {
	return c.BaseReactionHandler.PreHandleMatrixReaction(ctx, msg)
}

// HandleMatrixReaction implements bridgev2.ReactionHandlingNetworkAPI.
func (c *BaseClient) HandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (*database.Reaction, error) {
	return c.BaseReactionHandler.HandleMatrixReaction(ctx, msg)
}

// HandleMatrixReactionRemove implements bridgev2.ReactionHandlingNetworkAPI.
func (c *BaseClient) HandleMatrixReactionRemove(ctx context.Context, msg *bridgev2.MatrixReactionRemove) error {
	return c.BaseReactionHandler.HandleMatrixReactionRemove(ctx, msg)
}

// HandleMatrixMessageRemove implements bridgev2.RedactionHandlingNetworkAPI.
func (c *BaseClient) HandleMatrixMessageRemove(_ context.Context, _ *bridgev2.MatrixMessageRemove) error {
	return nil
}

// HandleMatrixTyping implements bridgev2.TypingHandlingNetworkAPI.
func (c *BaseClient) HandleMatrixTyping(_ context.Context, _ *bridgev2.MatrixTyping) error {
	return nil
}

// HandleMatrixRoomName implements bridgev2.RoomNameHandlingNetworkAPI.
func (c *BaseClient) HandleMatrixRoomName(_ context.Context, _ *bridgev2.MatrixRoomName) (bool, error) {
	return false, nil
}

// HandleMatrixRoomTopic implements bridgev2.RoomTopicHandlingNetworkAPI.
func (c *BaseClient) HandleMatrixRoomTopic(_ context.Context, _ *bridgev2.MatrixRoomTopic) (bool, error) {
	return false, nil
}

// FetchMessages implements bridgev2.BackfillingNetworkAPI.
func (c *BaseClient) FetchMessages(_ context.Context, _ bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	return nil, nil
}

// HandleMatrixDeleteChat implements bridgev2.DeleteChatHandlingNetworkAPI.
func (c *BaseClient) HandleMatrixDeleteChat(_ context.Context, _ *bridgev2.MatrixDeleteChat) error {
	return nil
}

// ResolveIdentifier implements bridgev2.IdentifierResolvingNetworkAPI.
func (c *BaseClient) ResolveIdentifier(_ context.Context, _ string, _ bool) (*bridgev2.ResolveIdentifierResponse, error) {
	return nil, nil
}

// GetApprovalHandler implements agentremote.ReactionTarget.
func (c *BaseClient) GetApprovalHandler() agentremote.ApprovalReactionHandler {
	return nil
}

// SetLoggedIn sets the logged-in state.
func (c *BaseClient) SetLoggedIn(v bool) {
	c.loggedIn.Store(v)
}

// HumanUserID returns the network user ID for the human user.
func (c *BaseClient) HumanUserID() networkid.UserID {
	if c.UserLogin == nil {
		return ""
	}
	return agentremote.HumanUserID(c.IDPrefix, c.UserLogin.ID)
}

// EnsureAgentGhost ensures the given agent ghost exists.
func (c *BaseClient) EnsureAgentGhost(ctx context.Context, agent *Agent) error {
	if agent == nil || c.UserLogin == nil {
		return nil
	}
	return agent.EnsureGhost(ctx, c.UserLogin)
}

// SendViaPortal sends a pre-built message through the bridge pipeline.
func (c *BaseClient) SendViaPortal(portal *bridgev2.Portal, sender bridgev2.EventSender, converted *bridgev2.ConvertedMessage) error {
	_, _, err := agentremote.SendViaPortal(agentremote.SendViaPortalParams{
		Login:     c.UserLogin,
		Portal:    portal,
		Sender:    sender,
		IDPrefix:  c.IDPrefix,
		LogKey:    c.LogKey,
		Converted: converted,
	})
	return err
}

// NewConversation creates a Conversation for the given portal.
func (c *BaseClient) NewConversation(ctx context.Context, portal *bridgev2.Portal) *Conversation {
	return newConversation(ctx, portal, c.UserLogin, bridgev2.EventSender{}, nil)
}

// StartTurn creates a new Turn for the given conversation.
func (c *BaseClient) StartTurn(ctx context.Context, conv *Conversation, agent *Agent, source *SourceRef) *Turn {
	return newTurn(ctx, conv, agent, source)
}
