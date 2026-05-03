package sdk

import (
	"context"
	"maps"
	"sync"

	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

type loginAwareClient interface {
	SetUserLogin(*bridgev2.UserLogin)
}

// NewConnectorBase builds an SDK-backed connector base that can be embedded by custom bridges.
func NewConnectorBase[SessionT SessionValue, ConfigDataT ConfigValue](cfg *Config[SessionT, ConfigDataT]) *ConnectorBase {
	mu, clientsRef := cfg.ClientCacheMu, cfg.ClientCache
	if mu == nil {
		mu = &sync.Mutex{}
	}
	if clientsRef == nil {
		clients := make(map[networkid.UserLoginID]bridgev2.NetworkAPI)
		clientsRef = &clients
	}

	protocolID := cfg.ProtocolID
	if protocolID == "" {
		protocolID = "sdk-" + cfg.Name
	}
	loadLogin := cfg.LoadLogin
	if loadLogin == nil {
		loadLogin = func(_ context.Context, login *bridgev2.UserLogin) error {
			return LoadUserLogin(login, LoadUserLoginConfig[bridgev2.NetworkAPI]{
				Mu:         mu,
				Clients:    *clientsRef,
				ClientsRef: clientsRef,
				BridgeName: cfg.Name,
				MakeBroken: cfg.MakeBrokenLogin,
				Accept:     cfg.AcceptLogin,
				Update: func(client bridgev2.NetworkAPI, login *bridgev2.UserLogin) {
					if cfg.UpdateClient != nil {
						cfg.UpdateClient(client, login)
						return
					}
					if typed, ok := client.(loginAwareClient); ok {
						typed.SetUserLogin(login)
					}
				},
				Create: func(login *bridgev2.UserLogin) (bridgev2.NetworkAPI, error) {
					if cfg.CreateClient != nil {
						return cfg.CreateClient(login)
					}
					return newSDKClient(login, cfg), nil
				},
				AfterLoad: func(client bridgev2.NetworkAPI) {
					if cfg.AfterLoadClient != nil {
						cfg.AfterLoadClient(client)
					}
				},
			})
		}
	}
	return NewConnector(ConnectorSpec{
		ProtocolID: protocolID,
		Init: func(bridge *bridgev2.Bridge) {
			mu.Lock()
			if *clientsRef == nil {
				*clientsRef = make(map[networkid.UserLoginID]bridgev2.NetworkAPI)
			}
			mu.Unlock()
			if cfg.InitConnector != nil {
				cfg.InitConnector(bridge)
			}
		},
		Start: func(ctx context.Context, bridge *bridgev2.Bridge) error {
			registerCommands(bridge, cfg)
			if cfg.StartConnector != nil {
				return cfg.StartConnector(ctx, bridge)
			}
			return nil
		},
		Stop: func(ctx context.Context, bridge *bridgev2.Bridge) {
			mu.Lock()
			cloned := maps.Clone(*clientsRef)
			mu.Unlock()
			for _, client := range cloned {
				client.Disconnect()
			}
			if cfg.StopConnector != nil {
				cfg.StopConnector(ctx, bridge)
			}
		},
		Name: func() bridgev2.BridgeName {
			if cfg.BridgeName != nil {
				return cfg.BridgeName()
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
			if cfg.ExampleConfig != "" {
				return cfg.ExampleConfig, cfg.ConfigData, cfg.ConfigUpgrader
			}
			return "{}", cfg.ConfigData, cfg.ConfigUpgrader
		},
		DBMeta: func() database.MetaTypes {
			if cfg.DBMeta != nil {
				return cfg.DBMeta()
			}
			return database.MetaTypes{}
		},
		Capabilities: func() *bridgev2.NetworkGeneralCapabilities {
			if cfg.NetworkCapabilities != nil {
				return cfg.NetworkCapabilities()
			}
			return &bridgev2.NetworkGeneralCapabilities{}
		},
		BridgeInfoVersion: func() (info, capabilities int) {
			if cfg.BridgeInfoVersion != nil {
				return cfg.BridgeInfoVersion()
			}
			return DefaultBridgeInfoVersion()
		},
		FillBridgeInfo: func(portal *bridgev2.Portal, content *event.BridgeEventContent) {
			if cfg.FillBridgeInfo != nil {
				cfg.FillBridgeInfo(portal, content)
				return
			}
			if portal == nil || content == nil || protocolID == "" {
				return
			}
			ApplyAgentRemoteBridgeInfo(content, protocolID, portal.RoomType)
		},
		LoadLogin: loadLogin,
		LoginFlows: func() []bridgev2.LoginFlow {
			if cfg.GetLoginFlows != nil {
				return cfg.GetLoginFlows()
			}
			return cfg.LoginFlows
		},
		CreateLogin: func(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
			if cfg.CreateLogin != nil {
				return cfg.CreateLogin(ctx, user, flowID)
			}
			return nil, bridgev2.ErrInvalidLoginFlowID
		},
	})
}
