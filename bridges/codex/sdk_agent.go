package codex

import "github.com/beeper/agentremote/sdk"

func codexSDKAgent() *sdk.Agent {
	return &sdk.Agent{
		ID:           string(codexGhostID),
		Name:         "Codex",
		Description:  "Codex agent",
		Identifiers:  []string{"codex"},
		ModelKey:     "codex",
		Capabilities: sdk.BaseAgentCapabilities(),
	}
}
