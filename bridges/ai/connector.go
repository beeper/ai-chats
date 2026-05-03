package ai

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"go.mau.fi/util/configupgrade"
	"go.mau.fi/util/dbutil"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/sdk"
)

const (
	defaultMaxContextMessages   = 20
	defaultGroupContextMessages = 20
	defaultMaxTokens            = 16384
	defaultReasoningEffort      = "low"
)

var (
	_ bridgev2.NetworkConnector               = (*OpenAIConnector)(nil)
	_ bridgev2.PortalBridgeInfoFillingNetwork = (*OpenAIConnector)(nil)
	_ bridgev2.IdentifierValidatingNetwork    = (*OpenAIConnector)(nil)
)

// OpenAIConnector wires mautrix bridgev2 to the OpenAI chat APIs.
type OpenAIConnector struct {
	br     *bridgev2.Bridge
	Config Config
	db     *dbutil.Database

	clientsMu sync.Mutex
	clients   map[networkid.UserLoginID]bridgev2.NetworkAPI
}

func (oc *OpenAIConnector) applyRuntimeDefaults() {
	if oc.Config.ModelCacheDuration == 0 {
		oc.Config.ModelCacheDuration = 6 * time.Hour
	}
	if oc.Config.Bridge.CommandPrefix == "" {
		oc.Config.Bridge.CommandPrefix = "!ai"
	}
}

func (oc *OpenAIConnector) ValidateUserID(id networkid.UserID) bool {
	if modelID := parseModelFromGhostID(string(id)); strings.TrimSpace(modelID) != "" {
		return resolveModelIDFromManifest(modelID) != ""
	}
	return false
}

func (oc *OpenAIConnector) Init(br *bridgev2.Bridge) {
	oc.br = br
	oc.db = nil
	oc.clientsMu.Lock()
	if oc.clients == nil {
		oc.clients = make(map[networkid.UserLoginID]bridgev2.NetworkAPI)
	}
	oc.clientsMu.Unlock()
	if br != nil && br.DB != nil && br.DB.Database != nil {
		oc.db = newBridgeChildDB(br.DB.Database, br.Log)
	}
}

func (oc *OpenAIConnector) Start(ctx context.Context) error {
	db := oc.bridgeDB()
	if db == nil {
		return fmt.Errorf("ai database not initialized")
	}
	if err := upgradeBridgeChildDB(ctx, db); err != nil {
		return err
	}
	oc.applyRuntimeDefaults()
	oc.registerCommands(oc.br)
	return nil
}

func (oc *OpenAIConnector) Stop() {
	oc.clientsMu.Lock()
	clients := maps.Clone(oc.clients)
	oc.clientsMu.Unlock()
	for _, client := range clients {
		if client != nil {
			client.Disconnect()
		}
	}
}

func (oc *OpenAIConnector) GetName() bridgev2.BridgeName {
	defaultCommandPrefix := "!ai"
	if trimmed := strings.TrimSpace(oc.Config.Bridge.CommandPrefix); trimmed != "" {
		defaultCommandPrefix = trimmed
	}
	return bridgev2.BridgeName{
		DisplayName:          "Beeper AI",
		NetworkURL:           "https://www.beeper.com/ai",
		NetworkIcon:          "mxc://beeper.com/51a668657dd9e0132cc823ad9402c6c2d0fc3321",
		NetworkID:            "ai",
		BeeperBridgeType:     "ai",
		DefaultPort:          29345,
		DefaultCommandPrefix: defaultCommandPrefix,
	}
}

func (oc *OpenAIConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		Portal:    func() any { return &PortalMetadata{} },
		Message:   func() any { return &MessageMetadata{} },
		UserLogin: func() any { return &UserLoginMetadata{} },
		Ghost:     func() any { return &GhostMetadata{} },
	}
}

func (oc *OpenAIConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{
		Provisioning: bridgev2.ProvisioningCapabilities{
			ResolveIdentifier: bridgev2.ResolveIdentifierCapabilities{
				CreateDM:       true,
				LookupUsername: true,
				ContactList:    true,
				Search:         true,
			},
		},
	}
}

func (oc *OpenAIConnector) GetConfig() (example string, data any, upgrader configupgrade.Upgrader) {
	return exampleNetworkConfig, &oc.Config, nil
}

func (oc *OpenAIConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	return oc.loadAIUserLogin(ctx, login, loginMetadata(login), nil)
}

func (oc *OpenAIConnector) GetLoginFlows() []bridgev2.LoginFlow {
	return oc.getLoginFlows()
}

func (oc *OpenAIConnector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	flows := oc.getLoginFlows()
	if !slices.ContainsFunc(flows, func(f bridgev2.LoginFlow) bool { return f.ID == flowID }) {
		return nil, bridgev2.ErrInvalidLoginFlowID
	}
	return &OpenAILogin{User: user, Connector: oc, FlowID: flowID}, nil
}

func (oc *OpenAIConnector) GetBridgeInfoVersion() (info, capabilities int) {
	return sdk.DefaultBridgeInfoVersion()
}

func (oc *OpenAIConnector) FillPortalBridgeInfo(portal *bridgev2.Portal, content *event.BridgeEventContent) {
	applyAIChatsBridgeInfo(portal, portalMeta(portal), content)
}

// Package-level flow definitions (use Provider* constants as flow IDs)
func (oc *OpenAIConnector) getLoginFlows() []bridgev2.LoginFlow {
	return []bridgev2.LoginFlow{
		{ID: ProviderMagicProxy, Name: "Magic Proxy"},
		{ID: FlowCustom, Name: "Manual"},
	}
}
