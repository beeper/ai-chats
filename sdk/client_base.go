package sdk

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote/turns"
)

type ClientBase struct {
	BaseReactionHandler

	loginMu sync.RWMutex
	login   *bridgev2.UserLogin

	loggedIn          atomic.Bool
	HumanUserIDPrefix string
	MessageIDPrefix   string
	MessageLogKey     string

	StreamMu                  sync.Mutex
	StreamSessions            map[string]*turns.StreamSession
	StreamFallbackToDebounced atomic.Bool
	streamClosing             atomic.Bool
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

func (c *ClientBase) HumanUserID() networkid.UserID {
	login := c.GetUserLogin()
	if login == nil || c.HumanUserIDPrefix == "" {
		return ""
	}
	return HumanUserID(c.HumanUserIDPrefix, login.ID)
}

func (c *ClientBase) InitStreamState() {
	c.StreamSessions = make(map[string]*turns.StreamSession)
	c.streamClosing.Store(false)
}

func (c *ClientBase) BeginStreamShutdown() {
	c.streamClosing.Store(true)
}

func (c *ClientBase) ResetStreamShutdown() {
	c.streamClosing.Store(false)
}

func (c *ClientBase) IsStreamShuttingDown() bool {
	return c.streamClosing.Load()
}

func (c *ClientBase) CloseAllSessions() {
	c.BeginStreamShutdown()
	c.StreamMu.Lock()
	sessions := make([]*turns.StreamSession, 0, len(c.StreamSessions))
	for _, sess := range c.StreamSessions {
		if sess != nil {
			sessions = append(sessions, sess)
		}
	}
	c.StreamSessions = make(map[string]*turns.StreamSession)
	c.StreamMu.Unlock()
	for _, sess := range sessions {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		sess.End(ctx, turns.EndReasonDisconnect)
		cancel()
	}
}
