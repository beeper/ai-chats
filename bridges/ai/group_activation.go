package ai

import "github.com/beeper/agentremote/pkg/shared/stringutil"

func (oc *AIClient) resolveGroupActivation(_ *PortalMetadata) string {
	if oc != nil && oc.connector != nil && oc.connector.Config.Messages != nil && oc.connector.Config.Messages.GroupChat != nil {
		if normalized, ok := stringutil.NormalizeEnum(oc.connector.Config.Messages.GroupChat.Activation, groupActivationAliases); ok {
			return normalized
		}
	}
	return "mention"
}
