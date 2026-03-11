package bridgeadapter

import (
	"fmt"
	"sync"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

// LoadUserLoginConfig configures the generic LoadUserLogin helper.
type LoadUserLoginConfig[C bridgev2.NetworkAPI] struct {
	Mu      *sync.Mutex
	Clients map[networkid.UserLoginID]bridgev2.NetworkAPI

	// BridgeName is used in error messages (e.g. "OpenCode").
	BridgeName string

	// MakeBroken returns a BrokenLoginClient for the given reason.
	// If nil, a default BrokenLoginClient is used.
	MakeBroken func(login *bridgev2.UserLogin, reason string) *BrokenLoginClient

	Update func(existing C, login *bridgev2.UserLogin)
	Create func(login *bridgev2.UserLogin) (C, error)

	// AfterLoad is called after a client is successfully loaded or created.
	// Optional — use for post-load setup like scheduling bootstrap.
	AfterLoad func(client C)
}

// LoadUserLogin loads or creates a typed client using LoadOrCreateTypedClient.
// On failure it assigns a BrokenLoginClient and returns nil error, matching the
// convention used by all bridge connectors.
func LoadUserLogin[C bridgev2.NetworkAPI](login *bridgev2.UserLogin, cfg LoadUserLoginConfig[C]) error {
	makeBroken := cfg.MakeBroken
	if makeBroken == nil {
		makeBroken = func(l *bridgev2.UserLogin, reason string) *BrokenLoginClient {
			return &BrokenLoginClient{UserLogin: l, Reason: reason}
		}
	}

	client, err := LoadOrCreateTypedClient(
		cfg.Mu, cfg.Clients, login, cfg.Update,
		func() (C, error) { return cfg.Create(login) },
	)
	if err != nil {
		login.Client = makeBroken(login, fmt.Sprintf("Couldn't initialize %s for this login.", cfg.BridgeName))
		return nil
	}
	login.Client = client
	if cfg.AfterLoad != nil {
		cfg.AfterLoad(client)
	}
	return nil
}
