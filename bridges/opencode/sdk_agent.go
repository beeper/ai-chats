package opencode

import (
	"strings"

	bridgesdk "github.com/beeper/agentremote/sdk"
)

// instanceDisplayName returns the display name for an OpenCode instance,
// falling back to "OpenCode" when the bridge is unavailable or the name is empty.
func (oc *OpenCodeClient) instanceDisplayName(instanceID string) string {
	if oc != nil && oc.bridge != nil {
		if name := strings.TrimSpace(oc.bridge.DisplayName(instanceID)); name != "" {
			return name
		}
	}
	return "OpenCode"
}

func openCodeSDKAgent(instanceID, displayName string) *bridgesdk.Agent {
	if displayName == "" {
		displayName = "OpenCode"
	}
	return &bridgesdk.Agent{
		ID:           string(OpenCodeUserID(instanceID)),
		Name:         displayName,
		Description:  "OpenCode instance",
		Identifiers:  []string{"opencode:" + instanceID},
		ModelKey:     "opencode:" + instanceID,
		Capabilities: bridgesdk.MultimodalAgentCapabilities(),
	}
}
