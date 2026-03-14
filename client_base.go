package agentremote

import (
	"context"
	"sync"
	"sync/atomic"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

type ClientBase struct {
	BaseReactionHandler
	BaseStreamState

	loginMu sync.RWMutex
	login   *bridgev2.UserLogin

	loggedIn          atomic.Bool
	HumanUserIDPrefix string
}

func (c *ClientBase) InitClientBase(login *bridgev2.UserLogin, target ReactionTarget) {
	c.SetUserLogin(login)
	c.BaseReactionHandler.Target = target
	c.InitStreamState()
}

func (c *ClientBase) SetUserLogin(login *bridgev2.UserLogin) {
	c.loginMu.Lock()
	c.login = login
	c.loginMu.Unlock()
}

func (c *ClientBase) GetUserLogin() *bridgev2.UserLogin {
	if c == nil {
		return nil
	}
	c.loginMu.RLock()
	defer c.loginMu.RUnlock()
	return c.login
}

func (c *ClientBase) Login() *bridgev2.UserLogin {
	return c.GetUserLogin()
}

// IsLoggedIn returns the current logged-in state.
func (c *ClientBase) IsLoggedIn() bool {
	return c.loggedIn.Load()
}

// SetLoggedIn sets the logged-in state.
func (c *ClientBase) SetLoggedIn(v bool) {
	c.loggedIn.Store(v)
}

// IsThisUser returns true if the given user ID matches the human user for this login.
func (c *ClientBase) IsThisUser(_ context.Context, userID networkid.UserID) bool {
	login := c.GetUserLogin()
	if login == nil || c.HumanUserIDPrefix == "" {
		return false
	}
	return userID == HumanUserID(c.HumanUserIDPrefix, login.ID)
}

func (c *ClientBase) BackgroundContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	if login := c.GetUserLogin(); login != nil && login.Bridge != nil && login.Bridge.BackgroundCtx != nil {
		return login.Bridge.BackgroundCtx
	}
	return context.Background()
}
