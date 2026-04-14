package sdk

import (
	"fmt"
	"strings"
	"sync"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

// LoadUserLoginConfig configures the generic LoadUserLogin helper.
type LoadUserLoginConfig[C bridgev2.NetworkAPI] struct {
	Mu         *sync.Mutex
	Clients    map[networkid.UserLoginID]bridgev2.NetworkAPI
	ClientsRef *map[networkid.UserLoginID]bridgev2.NetworkAPI

	// BridgeName is used in error messages (e.g. "OpenCode").
	BridgeName string

	// MakeBroken returns a BrokenLoginClient for the given reason.
	// If nil, a default BrokenLoginClient is used.
	MakeBroken func(login *bridgev2.UserLogin, reason string) *BrokenLoginClient
	Accept     func(*bridgev2.UserLogin) (ok bool, reason string)

	Update func(existing C, login *bridgev2.UserLogin)
	Create func(login *bridgev2.UserLogin) (C, error)

	// AfterLoad is called after a client is successfully loaded or created.
	// Optional — use for post-load setup like scheduling bootstrap.
	AfterLoad func(client C)
}

// LoadUserLogin loads or creates a typed client using the shared client cache.
// On failure it installs a BrokenLoginClient and returns nil so the bridge can
// keep the login visible while marking it unusable.
func LoadUserLogin[C bridgev2.NetworkAPI](login *bridgev2.UserLogin, cfg LoadUserLoginConfig[C]) error {
	makeBroken := cfg.MakeBroken
	if makeBroken == nil {
		makeBroken = func(l *bridgev2.UserLogin, reason string) *BrokenLoginClient {
			return NewBrokenLoginClient(l, reason)
		}
	}
	if cfg.Accept != nil {
		ok, reason := cfg.Accept(login)
		if !ok {
			if strings.TrimSpace(reason) == "" {
				reason = "This login is not supported."
			}
			login.Client = makeBroken(login, reason)
			return nil
		}
	}
	clients := cfg.Clients
	if cfg.ClientsRef != nil {
		clients = *cfg.ClientsRef
	}

	if login == nil {
		return fmt.Errorf("login is nil")
	}
	clientAPI, err := LoadOrCreateClient(
		cfg.Mu,
		clients,
		login.ID,
		func(existingAPI bridgev2.NetworkAPI) bool {
			existing, ok := existingAPI.(C)
			if !ok {
				return false
			}
			if cfg.Update != nil {
				cfg.Update(existing, login)
			}
			login.Client = existing
			return true
		},
		func() (bridgev2.NetworkAPI, error) {
			client, err := cfg.Create(login)
			if err != nil {
				return nil, err
			}
			login.Client = client
			return client, nil
		},
	)
	if err != nil {
		login.Client = makeBroken(login, fmt.Sprintf("Couldn't initialize %s for this login.", cfg.BridgeName))
		return nil
	}
	client, ok := clientAPI.(C)
	if !ok {
		login.Client = makeBroken(login, fmt.Sprintf("Couldn't initialize %s for this login.", cfg.BridgeName))
		return nil
	}
	login.Client = client
	if cfg.AfterLoad != nil {
		cfg.AfterLoad(client)
	}
	return nil
}
