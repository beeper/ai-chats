package agentremote

import (
	"context"

	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
)

type ConnectorSpec struct {
	ProtocolID string
	AIRoomKind string

	Init  func(*bridgev2.Bridge)
	Start func(context.Context) error
	Stop  func(context.Context)

	Name        func() bridgev2.BridgeName
	Config      func() (example string, data any, upgrader configupgrade.Upgrader)
	DBMeta      func() database.MetaTypes
	LoadLogin   func(context.Context, *bridgev2.UserLogin) error
	LoginFlows  func() []bridgev2.LoginFlow
	CreateLogin func(context.Context, *bridgev2.User, string) (bridgev2.LoginProcess, error)

	Capabilities      func() *bridgev2.NetworkGeneralCapabilities
	BridgeInfoVersion func() (info, capabilities int)
	FillBridgeInfo    func(*bridgev2.Portal, *event.BridgeEventContent)
}

type ConnectorBase struct {
	spec ConnectorSpec
	br   *bridgev2.Bridge
}

func NewConnector(spec ConnectorSpec) *ConnectorBase {
	if spec.AIRoomKind == "" {
		spec.AIRoomKind = AIRoomKindAgent
	}
	return &ConnectorBase{spec: spec}
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
	if c.spec.Init != nil {
		c.spec.Init(br)
	}
}

func (c *ConnectorBase) Start(ctx context.Context) error {
	if c == nil || c.spec.Start == nil {
		return nil
	}
	return c.spec.Start(ctx)
}

func (c *ConnectorBase) Stop(ctx context.Context) {
	if c == nil || c.spec.Stop == nil {
		return
	}
	c.spec.Stop(ctx)
}

func (c *ConnectorBase) GetName() bridgev2.BridgeName {
	if c == nil || c.spec.Name == nil {
		return bridgev2.BridgeName{}
	}
	return c.spec.Name()
}

func (c *ConnectorBase) GetConfig() (example string, data any, upgrader configupgrade.Upgrader) {
	if c == nil || c.spec.Config == nil {
		return "", nil, nil
	}
	return c.spec.Config()
}

func (c *ConnectorBase) GetDBMetaTypes() database.MetaTypes {
	if c == nil || c.spec.DBMeta == nil {
		return database.MetaTypes{}
	}
	return c.spec.DBMeta()
}

func (c *ConnectorBase) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	if c == nil || c.spec.Capabilities == nil {
		return DefaultNetworkCapabilities()
	}
	return c.spec.Capabilities()
}

func (c *ConnectorBase) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	if c == nil || c.spec.LoadLogin == nil {
		return nil
	}
	return c.spec.LoadLogin(ctx, login)
}

func (c *ConnectorBase) GetLoginFlows() []bridgev2.LoginFlow {
	if c == nil || c.spec.LoginFlows == nil {
		return nil
	}
	return c.spec.LoginFlows()
}

func (c *ConnectorBase) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if c == nil || c.spec.CreateLogin == nil {
		return nil, bridgev2.ErrInvalidLoginFlowID
	}
	return c.spec.CreateLogin(ctx, user, flowID)
}

func (c *ConnectorBase) GetBridgeInfoVersion() (info, capabilities int) {
	if c == nil || c.spec.BridgeInfoVersion == nil {
		return DefaultBridgeInfoVersion()
	}
	return c.spec.BridgeInfoVersion()
}

func (c *ConnectorBase) FillPortalBridgeInfo(portal *bridgev2.Portal, content *event.BridgeEventContent) {
	if c == nil {
		return
	}
	if c.spec.FillBridgeInfo != nil {
		c.spec.FillBridgeInfo(portal, content)
		return
	}
	if portal != nil && content != nil && c.spec.ProtocolID != "" {
		ApplyAgentRemoteBridgeInfo(content, c.spec.ProtocolID, portal.RoomType, c.spec.AIRoomKind)
	}
}
