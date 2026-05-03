package aihelpers

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

type ConnectorBase struct {
	br                *bridgev2.Bridge
	protocolID        string
	init              func(*bridgev2.Bridge)
	start             func(context.Context, *bridgev2.Bridge) error
	stop              func(context.Context, *bridgev2.Bridge)
	name              func() bridgev2.BridgeName
	config            func() (string, any, configupgrade.Upgrader)
	dbMeta            func() database.MetaTypes
	loadLogin         func(context.Context, *bridgev2.UserLogin) error
	loginFlows        func() []bridgev2.LoginFlow
	createLogin       func(context.Context, *bridgev2.User, string) (bridgev2.LoginProcess, error)
	capabilities      func() *bridgev2.NetworkGeneralCapabilities
	bridgeInfoVersion func() (int, int)
	fillBridgeInfo    func(*bridgev2.Portal, *event.BridgeEventContent)
}

// NewConnectorBase builds an AIHelper-backed connector base that can be embedded by custom bridges.
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
		protocolID = "ai-" + cfg.Name
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
					return newAIHelperClient(login, cfg), nil
				},
				AfterLoad: func(client bridgev2.NetworkAPI) {
					if cfg.AfterLoadClient != nil {
						cfg.AfterLoadClient(client)
					}
				},
			})
		}
	}
	return &ConnectorBase{
		protocolID: protocolID,
		init: func(bridge *bridgev2.Bridge) {
			mu.Lock()
			if *clientsRef == nil {
				*clientsRef = make(map[networkid.UserLoginID]bridgev2.NetworkAPI)
			}
			mu.Unlock()
			if cfg.InitConnector != nil {
				cfg.InitConnector(bridge)
			}
		},
		start: func(ctx context.Context, bridge *bridgev2.Bridge) error {
			registerCommands(bridge, cfg)
			if cfg.StartConnector != nil {
				return cfg.StartConnector(ctx, bridge)
			}
			return nil
		},
		stop: func(ctx context.Context, bridge *bridgev2.Bridge) {
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
		name: func() bridgev2.BridgeName {
			if cfg.BridgeName != nil {
				return cfg.BridgeName()
			}
			port := cfg.Port
			if port == 0 {
				port = 29400
			}
			return bridgev2.BridgeName{
				DisplayName:      cfg.Name,
				NetworkURL:       "https://github.com/beeper/ai-chats",
				NetworkID:        cfg.Name,
				BeeperBridgeType: cfg.Name,
				DefaultPort:      uint16(port),
			}
		},
		config: func() (string, any, configupgrade.Upgrader) {
			if cfg.ExampleConfig != "" {
				return cfg.ExampleConfig, cfg.ConfigData, cfg.ConfigUpgrader
			}
			return "{}", cfg.ConfigData, cfg.ConfigUpgrader
		},
		dbMeta: func() database.MetaTypes {
			if cfg.DBMeta != nil {
				return cfg.DBMeta()
			}
			return database.MetaTypes{}
		},
		capabilities: func() *bridgev2.NetworkGeneralCapabilities {
			if cfg.NetworkCapabilities != nil {
				return cfg.NetworkCapabilities()
			}
			return &bridgev2.NetworkGeneralCapabilities{}
		},
		bridgeInfoVersion: func() (int, int) {
			if cfg.BridgeInfoVersion != nil {
				return cfg.BridgeInfoVersion()
			}
			return DefaultBridgeInfoVersion()
		},
		fillBridgeInfo: func(portal *bridgev2.Portal, content *event.BridgeEventContent) {
			if cfg.FillBridgeInfo != nil {
				cfg.FillBridgeInfo(portal, content)
				return
			}
			if portal != nil && content != nil && protocolID != "" {
				ApplyAIChatsBridgeInfo(content, protocolID, portal.RoomType)
			}
		},
		loadLogin: loadLogin,
		loginFlows: func() []bridgev2.LoginFlow {
			if cfg.GetLoginFlows != nil {
				return cfg.GetLoginFlows()
			}
			return cfg.LoginFlows
		},
		createLogin: func(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
			if cfg.CreateLogin != nil {
				return cfg.CreateLogin(ctx, user, flowID)
			}
			return nil, bridgev2.ErrInvalidLoginFlowID
		},
	}
}

func (c *ConnectorBase) Bridge() *bridgev2.Bridge {
	if c == nil {
		return nil
	}
	return c.br
}

func (c *ConnectorBase) Init(br *bridgev2.Bridge) {
	if c == nil {
		return
	}
	c.br = br
	if c.init != nil {
		c.init(br)
	}
}

func (c *ConnectorBase) Start(ctx context.Context) error {
	if c == nil || c.start == nil {
		return nil
	}
	return c.start(ctx, c.br)
}

func (c *ConnectorBase) Stop(ctx context.Context) {
	if c == nil || c.stop == nil {
		return
	}
	c.stop(ctx, c.br)
}

func (c *ConnectorBase) GetName() bridgev2.BridgeName {
	if c == nil || c.name == nil {
		return bridgev2.BridgeName{}
	}
	return c.name()
}

func (c *ConnectorBase) GetConfig() (string, any, configupgrade.Upgrader) {
	if c == nil || c.config == nil {
		return "", nil, nil
	}
	return c.config()
}

func (c *ConnectorBase) GetDBMetaTypes() database.MetaTypes {
	if c == nil || c.dbMeta == nil {
		return database.MetaTypes{}
	}
	return c.dbMeta()
}

func (c *ConnectorBase) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	if c == nil || c.capabilities == nil {
		return &bridgev2.NetworkGeneralCapabilities{}
	}
	return c.capabilities()
}

func (c *ConnectorBase) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	if c == nil || c.loadLogin == nil {
		return nil
	}
	return c.loadLogin(ctx, login)
}

func (c *ConnectorBase) GetLoginFlows() []bridgev2.LoginFlow {
	if c == nil || c.loginFlows == nil {
		return nil
	}
	return c.loginFlows()
}

func (c *ConnectorBase) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if c == nil || c.createLogin == nil {
		return nil, bridgev2.ErrInvalidLoginFlowID
	}
	return c.createLogin(ctx, user, flowID)
}

func (c *ConnectorBase) GetBridgeInfoVersion() (int, int) {
	if c == nil || c.bridgeInfoVersion == nil {
		return DefaultBridgeInfoVersion()
	}
	return c.bridgeInfoVersion()
}

func (c *ConnectorBase) FillPortalBridgeInfo(portal *bridgev2.Portal, content *event.BridgeEventContent) {
	if c == nil {
		return
	}
	if c.fillBridgeInfo != nil {
		c.fillBridgeInfo(portal, content)
		return
	}
	if portal != nil && content != nil && c.protocolID != "" {
		ApplyAIChatsBridgeInfo(content, c.protocolID, portal.RoomType)
	}
}
