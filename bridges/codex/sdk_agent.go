package codex

import bridgesdk "github.com/beeper/agentremote/sdk"

func codexSDKAgent() *bridgesdk.Agent {
	return &bridgesdk.Agent{
		ID:           string(codexGhostID),
		Name:         "Codex",
		Description:  "Codex agent",
		Identifiers:  []string{"codex"},
		ModelKey:     "codex",
		Capabilities: bridgesdk.BaseAgentCapabilities(),
	}
}
