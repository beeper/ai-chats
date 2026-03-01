package connector

import (
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

func (oc *AIClient) resolveGroupActivation(meta *PortalMetadata) string {
	if meta != nil {
		if normalized, ok := stringutil.NormalizeEnum(meta.GroupActivation, groupActivationAliases); ok {
			return normalized
		}
	}
	if oc != nil && oc.connector != nil && oc.connector.Config.Messages != nil && oc.connector.Config.Messages.GroupChat != nil {
		if normalized, ok := stringutil.NormalizeEnum(oc.connector.Config.Messages.GroupChat.Activation, groupActivationAliases); ok {
			return normalized
		}
	}
	return "mention"
}

func normalizeSendPolicyMode(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "deny" || value == "off" {
		return "deny"
	}
	if value == "allow" || value == "on" {
		return "allow"
	}
	return ""
}
