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

// NewBrokenLoginClient creates a BrokenLoginClient for a login that cannot be fully
// initialized (e.g. missing credentials or invalid config).
func NewBrokenLoginClient(login *bridgev2.UserLogin, reason string) *BrokenLoginClient {
	return &BrokenLoginClient{UserLogin: login, Reason: reason}
}

var _ bridgev2.NetworkAPI = (*BrokenLoginClient)(nil)

func (c *BrokenLoginClient) Connect(_ context.Context) {
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

func (c *BrokenLoginClient) IsThisUser(_ context.Context, _ networkid.UserID) bool {
	return false
}

func (c *BrokenLoginClient) GetChatInfo(_ context.Context, _ *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	return nil, bridgev2.ErrNotLoggedIn
}

func (c *BrokenLoginClient) GetUserInfo(_ context.Context, _ *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	return nil, bridgev2.ErrNotLoggedIn
}

func (c *BrokenLoginClient) GetCapabilities(_ context.Context, _ *bridgev2.Portal) *event.RoomFeatures {
	return &event.RoomFeatures{}
}

func (c *BrokenLoginClient) HandleMatrixMessage(_ context.Context, _ *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	return nil, bridgev2.ErrNotLoggedIn
}
