package connector

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.mau.fi/util/configupgrade"
	"go.mau.fi/util/dbutil"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/pkg/aidb"
	"github.com/beeper/agentremote/pkg/bridgeadapter"
	airuntime "github.com/beeper/agentremote/pkg/runtime"
)

const (
	defaultTemperature          = 0.0 // Unset by default; provider/model default is used.
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

func (oc *OpenAIConnector) Init(bridge *bridgev2.Bridge) {
	// Process remote events synchronously so callers can retrieve event IDs
	// and maintain strict message ordering (send → edit → redact).
	bridgev2.PortalEventBuffer = 0

	oc.br = bridge
	oc.db = nil
	if bridge != nil && bridge.DB != nil && bridge.DB.Database != nil {
		oc.db = aidb.NewChild(
			bridge.DB.Database,
			dbutil.ZeroLogger(bridge.Log.With().Str("db_section", "ai_bridge").Logger()),
		)
	}
	bridgeadapter.EnsureClientMap(&oc.clientsMu, &oc.clients)
}

func (oc *OpenAIConnector) Stop(ctx context.Context) {
	bridgeadapter.StopClients(&oc.clientsMu, &oc.clients)
}

func (oc *OpenAIConnector) Start(ctx context.Context) error {
	db := oc.bridgeDB()
	if err := aidb.Upgrade(ctx, db, "ai_bridge", "ai bridge database not initialized"); err != nil {
		return err
	}

	oc.applyRuntimeDefaults()

	// Ensure all stored logins are loaded into the process-local cache early.
	// bridgev2's provisioning logout endpoint uses GetCachedUserLoginByID, so if logins
	// haven't been loaded yet, clients may be unable to remove accounts.
	oc.primeUserLoginCache(ctx)
	if _, err := oc.reconcileManagedBeeperLogin(ctx); err != nil {
		return err
	}

	// Register AI commands with the command processor
	if proc, ok := oc.br.Commands.(*commands.Processor); ok {
		oc.registerCommands(proc)
		oc.br.Log.Info().Msg("Registered AI commands with command processor")
	} else {
		oc.br.Log.Warn().Type("commands_type", oc.br.Commands).Msg("Failed to register AI commands: command processor type assertion failed")
	}

	// Register custom Matrix event handlers
	oc.registerCustomEventHandlers()

	// Initialize provisioning API endpoints
	oc.initProvisioning()

	return nil
}

func (oc *OpenAIConnector) primeUserLoginCache(ctx context.Context) {
	if oc == nil {
		return
	}
	bridgeadapter.PrimeUserLoginCache(ctx, oc.br)
}

func (oc *OpenAIConnector) applyRuntimeDefaults() {
	if oc.Config.ModelCacheDuration == 0 {
		oc.Config.ModelCacheDuration = 6 * time.Hour
	}
	if oc.Config.Bridge.CommandPrefix == "" {
		oc.Config.Bridge.CommandPrefix = "!ai"
	}
	if oc.Config.Pruning == nil {
		oc.Config.Pruning = airuntime.DefaultPruningConfig()
	} else {
		oc.Config.Pruning = airuntime.ApplyPruningDefaults(oc.Config.Pruning)
	}
}

// registerCustomEventHandlers registers connector-owned event handlers.
func (oc *OpenAIConnector) registerCustomEventHandlers() {
	if !registerScheduleTickEventHandler(oc.br, oc.handleScheduleTickEvent) {
		oc.br.Log.Warn().Msg("Cannot register custom event handlers: Matrix connector type assertion failed")
		return
	}

	oc.br.Log.Info().Msg("Registered connector event handlers")
}

func (oc *OpenAIConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return bridgeadapter.DefaultNetworkCapabilities()
}

func (oc *OpenAIConnector) ValidateUserID(id networkid.UserID) bool {
	if modelID := parseModelFromGhostID(string(id)); strings.TrimSpace(modelID) != "" {
		return resolveModelIDFromManifest(modelID) != ""
	}
	if agentID, ok := parseAgentFromGhostID(string(id)); ok && isValidAgentID(strings.TrimSpace(agentID)) {
		return true
	}
	return false
}

func (oc *OpenAIConnector) GetBridgeInfoVersion() (info, capabilities int) {
	// Bump capabilities version when room features change.
	// v2: Added UpdateBridgeInfo call on model switch to properly broadcast capability changes
	return bridgeadapter.DefaultBridgeInfoVersion()
}

// FillPortalBridgeInfo sets bridge metadata for AI rooms.
func (oc *OpenAIConnector) FillPortalBridgeInfo(portal *bridgev2.Portal, content *event.BridgeEventContent) {
	applyAIBridgeInfo(portal, portalMeta(portal), content)
}

func (oc *OpenAIConnector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName:          "Beeper Cloud",
		NetworkURL:           "https://www.beeper.com/ai",
		NetworkIcon:          "mxc://beeper.com/51a668657dd9e0132cc823ad9402c6c2d0fc3321",
		NetworkID:            "ai",
		BeeperBridgeType:     "ai",
		DefaultPort:          29345,
		DefaultCommandPrefix: oc.Config.Bridge.CommandPrefix,
	}
}

func (oc *OpenAIConnector) GetConfig() (example string, data any, upgrader configupgrade.Upgrader) {
	return exampleNetworkConfig, &oc.Config, configupgrade.SimpleUpgrader(upgradeConfig)
}

func (oc *OpenAIConnector) GetDBMetaTypes() database.MetaTypes {
	return bridgeadapter.BuildMetaTypes(
		func() any { return &PortalMetadata{} },
		func() any { return &MessageMetadata{} },
		func() any { return &UserLoginMetadata{} },
		func() any { return &GhostMetadata{} },
	)
}

func (oc *OpenAIConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	_ = ctx
	meta := loginMetadata(login)
	return oc.loadAIUserLogin(login, meta)
}

// Package-level flow definitions (use Provider* constants as flow IDs)
func (oc *OpenAIConnector) GetLoginFlows() []bridgev2.LoginFlow {
	flows := make([]bridgev2.LoginFlow, 0, 3)
	if !oc.hasManagedBeeperAuth() {
		flows = append(flows, bridgev2.LoginFlow{ID: ProviderBeeper, Name: "Beeper Cloud"})
	}
	flows = append(flows,
		bridgev2.LoginFlow{ID: ProviderMagicProxy, Name: "Magic Proxy"},
		bridgev2.LoginFlow{ID: FlowCustom, Name: "Manual"},
	)
	return flows
}

func (oc *OpenAIConnector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	// Validate by checking if flowID is in available flows
	flows := oc.GetLoginFlows()
	valid := false
	for _, f := range flows {
		if f.ID == flowID {
			valid = true
			break
		}
	}
	if !valid {
		return nil, fmt.Errorf("login flow %s is not available", flowID)
	}
	return &OpenAILogin{User: user, Connector: oc, FlowID: flowID}, nil
}
