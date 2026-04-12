package openclaw

import (
	"strings"

	"github.com/beeper/agentremote/sdk"
)

func (oc *OpenClawClient) sdkAgentForProfile(profile openClawAgentProfile) *sdk.Agent {
	displayName := oc.displayNameFromAgentProfile(profile)
	agentID := strings.TrimSpace(profile.AgentID)
	return &sdk.Agent{
		ID:           string(openClawGhostUserID(agentID)),
		Name:         displayName,
		Description:  "OpenClaw agent",
		AvatarURL:    profile.AvatarURL,
		Identifiers:  oc.configuredAgentIdentifiers(agentID),
		ModelKey:     agentID,
		Capabilities: sdk.BaseAgentCapabilities(),
	}
}
