package ai

import (
	"strings"
	"sync"
	"time"

	"go.mau.fi/util/dbutil"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
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
	*sdk.ConnectorBase
	br        *bridgev2.Bridge
	Config    Config
	db        *dbutil.Database
	sdkConfig *sdk.Config[*AIClient, *Config]

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
	if oc.Config.Agents == nil {
		oc.Config.Agents = &AgentsConfig{}
	}
	if oc.Config.Agents.Defaults == nil {
		oc.Config.Agents.Defaults = &AgentDefaultsConfig{}
	}
	if oc.Config.Agents.Defaults.Compaction == nil {
		oc.Config.Agents.Defaults.Compaction = airuntime.DefaultPruningConfig()
	} else {
		oc.Config.Agents.Defaults.Compaction = airuntime.ApplyPruningDefaults(oc.Config.Agents.Defaults.Compaction)
	}
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
