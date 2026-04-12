package sdk

import (
	"context"
	"fmt"
	"sync"

	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

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
		loadLogin = TypedClientLoader(TypedClientLoaderSpec[bridgev2.NetworkAPI]{
			Accept: cfg.AcceptLogin,
			LoadUserLoginConfig: LoadUserLoginConfig[bridgev2.NetworkAPI]{
				Mu:         mu,
				Clients:    *clientsRef,
				ClientsRef: clientsRef,
				BridgeName: cfg.Name,
				MakeBroken: cfg.MakeBrokenLogin,
				Update: func(client bridgev2.NetworkAPI, login *bridgev2.UserLogin) {
					if cfg.UpdateClient != nil {
						cfg.UpdateClient(client, login)
						return
					}
					if typed, ok := client.(*sdkClient[SessionT, ConfigDataT]); ok {
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
			},
		})
	}
	return NewConnector(ConnectorSpec{
		ProtocolID: protocolID,
		Init: func(bridge *bridgev2.Bridge) {
			EnsureClientMap(mu, clientsRef)
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
			StopClients(mu, clientsRef)
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
			return database.MetaTypes{
				Portal:    func() any { return &map[string]any{} },
				Message:   func() any { return &map[string]any{} },
				UserLogin: func() any { return &map[string]any{} },
				Ghost:     func() any { return &map[string]any{} },
			}
		},
		Capabilities: func() *bridgev2.NetworkGeneralCapabilities {
			if cfg.NetworkCapabilities != nil {
				return cfg.NetworkCapabilities()
			}
			return DefaultNetworkCapabilities()
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
			ApplyAgentRemoteBridgeInfo(content, protocolID, portal.RoomType, AIRoomKindAgent)
		},
		LoadLogin: loadLogin,
		LoginFlows: func() []bridgev2.LoginFlow {
			if cfg.GetLoginFlows != nil {
				return cfg.GetLoginFlows()
			}
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
}
