package bridgeadapter

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
)

// BrokenLoginClient keeps invalid logins loadable/deletable.
type BrokenLoginClient struct {
	UserLogin *bridgev2.UserLogin
	Reason    string
	OnLogout  func(context.Context, *bridgev2.UserLogin)
}

var _ bridgev2.NetworkAPI = (*BrokenLoginClient)(nil)

func (c *BrokenLoginClient) Connect(ctx context.Context) {
	_ = ctx
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

func (c *BrokenLoginClient) Disconnect() {}

func (c *BrokenLoginClient) IsLoggedIn() bool { return false }

func (c *BrokenLoginClient) LogoutRemote(ctx context.Context) {
	if c != nil && c.OnLogout != nil && c.UserLogin != nil {
		c.OnLogout(ctx, c.UserLogin)
	}
}

func (c *BrokenLoginClient) IsThisUser(ctx context.Context, userID networkid.UserID) bool {
	_ = ctx
	_ = userID
	return false
}

func (c *BrokenLoginClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	_ = ctx
	_ = portal
	return nil, bridgev2.ErrNotLoggedIn
}

func (c *BrokenLoginClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	_ = ctx
	_ = ghost
	return nil, bridgev2.ErrNotLoggedIn
}

func (c *BrokenLoginClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	_ = ctx
	_ = portal
	return &event.RoomFeatures{}
}

func (c *BrokenLoginClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	_ = ctx
	_ = msg
	return nil, bridgev2.ErrNotLoggedIn
}
