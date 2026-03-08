package connector

import "github.com/beeper/ai-bridge/pkg/shared/stringutil"

func (oc *AIClient) resolveGroupActivation(meta *PortalMetadata) string {
	_ = meta
	if oc != nil && oc.connector != nil && oc.connector.Config.Messages != nil && oc.connector.Config.Messages.GroupChat != nil {
		if normalized, ok := stringutil.NormalizeEnum(oc.connector.Config.Messages.GroupChat.Activation, groupActivationAliases); ok {
			return normalized
		}
	}
	return "mention"
}
