package ai

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"go.mau.fi/util/dbutil"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote"
	airuntime "github.com/beeper/agentremote/pkg/runtime"
	bridgesdk "github.com/beeper/agentremote/sdk"
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
	*agentremote.ConnectorBase
	br        *bridgev2.Bridge
	Config    Config
	db        *dbutil.Database
	sdkConfig *bridgesdk.Config

	clientsMu sync.Mutex
	clients   map[networkid.UserLoginID]bridgev2.NetworkAPI
}

func (oc *OpenAIConnector) primeUserLoginCache(ctx context.Context) {
	if oc == nil {
		return
	}
	agentremote.PrimeUserLoginCache(ctx, oc.br)
}

func (oc *OpenAIConnector) applyRuntimeDefaults() {
	if oc.Config.ModelCacheDuration == 0 {
		oc.Config.ModelCacheDuration = 6 * time.Hour
	}
	bridgesdk.ApplyDefaultCommandPrefix(&oc.Config.Bridge.CommandPrefix, "!ai")
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

func (oc *OpenAIConnector) ValidateUserID(id networkid.UserID) bool {
	if modelID := parseModelFromGhostID(string(id)); strings.TrimSpace(modelID) != "" {
		return resolveModelIDFromManifest(modelID) != ""
	}
	if agentID, ok := parseAgentFromGhostID(string(id)); ok && isValidAgentID(strings.TrimSpace(agentID)) {
		return true
	}
	return false
}

// Package-level flow definitions (use Provider* constants as flow IDs)
func (oc *OpenAIConnector) getLoginFlows() []bridgev2.LoginFlow {
	return []bridgev2.LoginFlow{
		{ID: ProviderMagicProxy, Name: "Magic Proxy"},
		{ID: FlowCustom, Name: "Manual"},
	}
}

func (oc *OpenAIConnector) createLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	flows := oc.getLoginFlows()
	if !slices.ContainsFunc(flows, func(f bridgev2.LoginFlow) bool { return f.ID == flowID }) {
		return nil, fmt.Errorf("login flow %s is not available", flowID)
	}
	return &OpenAILogin{User: user, Connector: oc, FlowID: flowID}, nil
}
