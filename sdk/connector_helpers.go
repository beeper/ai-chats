package sdk

import (
	"context"
	"sync"

	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type loginAwareClient interface {
	SetUserLogin(*bridgev2.UserLogin)
}

type StandardConnectorConfigParams[SessionT SessionValue, ConfigDataT ConfigValue, PortalT, MessageT, LoginT, GhostT any] struct {
	Name                 string
	Description          string
	ProtocolID           string
	ProviderIdentity     ProviderIdentity
	ClientCacheMu        *sync.Mutex
	ClientCache          *map[networkid.UserLoginID]bridgev2.NetworkAPI
	AgentCatalog         AgentCatalog
	GetCapabilities      func(session SessionT, conv *Conversation) *RoomFeatures
	InitConnector        func(br *bridgev2.Bridge)
	StartConnector       func(ctx context.Context, br *bridgev2.Bridge) error
	StopConnector        func(ctx context.Context, br *bridgev2.Bridge)
	DisplayName          string
	NetworkURL           string
	NetworkIcon          string
	NetworkID            string
	BeeperBridgeType     string
	DefaultPort          uint16
	DefaultCommandPrefix func() string
	ExampleConfig        string
	ConfigData           ConfigDataT
	ConfigUpgrader       configupgrade.Upgrader
	NewPortal            func() PortalT
	NewMessage           func() MessageT
	NewLogin             func() LoginT
	NewGhost             func() GhostT
	NetworkCapabilities  func() *bridgev2.NetworkGeneralCapabilities
	FillBridgeInfo       func(portal *bridgev2.Portal, content *event.BridgeEventContent)
	AcceptLogin          func(login *bridgev2.UserLogin) (bool, string)
	MakeBrokenLogin      func(login *bridgev2.UserLogin, reason string) *BrokenLoginClient
	LoadLogin            func(ctx context.Context, login *bridgev2.UserLogin) error
	CreateClient         func(login *bridgev2.UserLogin) (bridgev2.NetworkAPI, error)
	UpdateClient         func(client bridgev2.NetworkAPI, login *bridgev2.UserLogin)
	AfterLoadClient      func(client bridgev2.NetworkAPI)
	LoginFlows           []bridgev2.LoginFlow
	GetLoginFlows        func() []bridgev2.LoginFlow
	CreateLogin          func(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error)
}

// NewStandardConnectorConfig builds the common bridgesdk.Config skeleton used by
// the dedicated bridge connectors.
func NewStandardConnectorConfig[SessionT SessionValue, ConfigDataT ConfigValue, PortalT, MessageT, LoginT, GhostT any](p StandardConnectorConfigParams[SessionT, ConfigDataT, PortalT, MessageT, LoginT, GhostT]) *Config[SessionT, ConfigDataT] {
	return &Config[SessionT, ConfigDataT]{
		Name:             p.Name,
		Description:      p.Description,
		ProtocolID:       p.ProtocolID,
		AgentCatalog:     p.AgentCatalog,
		ProviderIdentity: p.ProviderIdentity,
		ClientCacheMu:    p.ClientCacheMu,
		ClientCache:      p.ClientCache,
		GetCapabilities:  p.GetCapabilities,
		InitConnector:    p.InitConnector,
		StartConnector:   p.StartConnector,
		StopConnector:    p.StopConnector,
		BridgeName: func() bridgev2.BridgeName {
			return bridgev2.BridgeName{
				DisplayName:          p.DisplayName,
				NetworkURL:           p.NetworkURL,
				NetworkIcon:          id.ContentURIString(p.NetworkIcon),
				NetworkID:            p.NetworkID,
				BeeperBridgeType:     p.BeeperBridgeType,
				DefaultPort:          p.DefaultPort,
				DefaultCommandPrefix: p.DefaultCommandPrefix(),
			}
		},
		ExampleConfig:  p.ExampleConfig,
		ConfigData:     p.ConfigData,
		ConfigUpgrader: p.ConfigUpgrader,
		DBMeta: func() database.MetaTypes {
			return database.MetaTypes{
				Portal:    func() any { return p.NewPortal() },
				Message:   func() any { return p.NewMessage() },
				UserLogin: func() any { return p.NewLogin() },
				Ghost:     func() any { return p.NewGhost() },
			}
		},
		NetworkCapabilities: p.NetworkCapabilities,
		FillBridgeInfo:      p.FillBridgeInfo,
		AcceptLogin:         p.AcceptLogin,
		MakeBrokenLogin:     p.MakeBrokenLogin,
		LoadLogin:           p.LoadLogin,
		CreateClient:        p.CreateClient,
		UpdateClient:        p.UpdateClient,
		AfterLoadClient:     p.AfterLoadClient,
		LoginFlows:          p.LoginFlows,
		GetLoginFlows:       p.GetLoginFlows,
		CreateLogin:         p.CreateLogin,
	}
}
