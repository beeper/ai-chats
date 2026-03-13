package sdk

import (
	"context"
	"fmt"
	"sync"

	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote"
)

type sdkConnector struct {
	*agentremote.ConnectorBase
	cfg     *Config
	br      *bridgev2.Bridge
	mu      sync.Mutex
	clients map[networkid.UserLoginID]bridgev2.NetworkAPI
}

func newSDKConnector(cfg *Config) *sdkConnector {
	sc := &sdkConnector{cfg: cfg}
	protocolID := cfg.ProtocolID
	if protocolID == "" {
		protocolID = "sdk-" + cfg.Name
	}
	sc.ConnectorBase = agentremote.NewConnector(agentremote.ConnectorSpec{
		ProtocolID: protocolID,
		Init: func(br *bridgev2.Bridge) {
			sc.br = br
			agentremote.EnsureClientMap(&sc.mu, &sc.clients)
		},
		Start: func(context.Context) error {
			registerCommands(sc.br, cfg)
			return nil
		},
		Stop: func(context.Context) {
			agentremote.StopClients(&sc.mu, &sc.clients)
		},
		Name: func() bridgev2.BridgeName {
			desc := cfg.Description
			if desc == "" {
				desc = fmt.Sprintf("A Matrix↔%s bridge for Beeper.", cfg.Name)
			}
			port := cfg.Port
			if port == 0 {
				port = 29400
			}
			return bridgev2.BridgeName{
				DisplayName:      cfg.Name,
				NetworkURL:       "https://github.com/beeper/agentremote",
				NetworkID:        cfg.Name,
				BeeperBridgeType: cfg.Name,
				DefaultPort:      uint16(port),
			}
		},
		Config: func() (string, any, configupgrade.Upgrader) {
			example := cfg.ExampleConfig
			if example == "" {
				example = "{}"
			}
			return example, cfg.ConfigData, cfg.ConfigUpgrader
		},
		DBMeta: func() database.MetaTypes {
			if cfg.DBMeta != nil {
				return cfg.DBMeta()
			}
			return database.MetaTypes{
				Portal:    func() any { return &map[string]any{} },
				Message:   func() any { return &map[string]any{} },
				UserLogin: func() any { return &map[string]any{} },
				Ghost:     func() any { return &map[string]any{} },
			}
		},
		Capabilities: func() *bridgev2.NetworkGeneralCapabilities {
			return agentremote.DefaultNetworkCapabilities()
		},
		LoadLogin: agentremote.TypedClientLoader(agentremote.TypedClientLoaderSpec[*sdkClient]{
			Accept: func(_ *bridgev2.UserLogin) (bool, string) {
				return true, ""
			},
			LoadUserLoginConfig: agentremote.LoadUserLoginConfig[*sdkClient]{
				Mu:         &sc.mu,
				Clients:    sc.clients,
				BridgeName: cfg.Name,
				Update: func(c *sdkClient, l *bridgev2.UserLogin) {
					c.SetUserLogin(l)
				},
				Create: func(l *bridgev2.UserLogin) (*sdkClient, error) {
					return newSDKClient(l, sc), nil
				},
			},
		}),
		LoginFlows: func() []bridgev2.LoginFlow {
			if len(cfg.LoginFlows) > 0 {
				return cfg.LoginFlows
			}
			return []bridgev2.LoginFlow{{
				ID:          "sdk-default",
				Name:        cfg.Name,
				Description: fmt.Sprintf("Login to %s", cfg.Name),
			}}
		},
		CreateLogin: func(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
			if cfg.CreateLogin != nil {
				return cfg.CreateLogin(ctx, user, flowID)
			}
			if flowID == "sdk-default" {
				return &sdkAutoLogin{user: user}, nil
			}
			return nil, bridgev2.ErrInvalidLoginFlowID
		},
	})
	return sc
}
