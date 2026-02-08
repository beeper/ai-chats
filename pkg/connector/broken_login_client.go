package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
)

// brokenLoginClient is used when a stored login can't be fully initialized (e.g. missing credentials
// or invalid config). bridgev2 won't cache logins if LoadUserLogin returns an error, which makes them
// impossible to delete via provisioning. This client keeps the login loadable and deletable.
type brokenLoginClient struct {
	UserLogin *bridgev2.UserLogin
	Reason    string
}

var _ bridgev2.NetworkAPI = (*brokenLoginClient)(nil)

func (c *brokenLoginClient) Connect(ctx context.Context) {
	if c == nil || c.UserLogin == nil || c.UserLogin.BridgeState == nil {
		return
	}
	msg := c.Reason
	if msg == "" {
		msg = "Login is not usable. Sign in again or remove this account."
	}
	c.UserLogin.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateBadCredentials,
		Message:    msg,
	})
}

func (c *brokenLoginClient) Disconnect() {}

func (c *brokenLoginClient) IsLoggedIn() bool { return false }

func (c *brokenLoginClient) LogoutRemote(ctx context.Context) {}

func (c *brokenLoginClient) IsThisUser(ctx context.Context, userID networkid.UserID) bool { return false }

func (c *brokenLoginClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	return nil, bridgev2.ErrNotLoggedIn
}

func (c *brokenLoginClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	return nil, bridgev2.ErrNotLoggedIn
}

func (c *brokenLoginClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	return &event.RoomFeatures{}
}

func (c *brokenLoginClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	return nil, bridgev2.ErrNotLoggedIn
}

