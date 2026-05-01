package ai

func (oc *AIClient) resolveGroupActivation(_ *PortalMetadata) string {
	if oc != nil && oc.connector != nil && oc.connector.Config.Messages != nil && oc.connector.Config.Messages.GroupChat != nil {
		switch oc.connector.Config.Messages.GroupChat.Activation {
		case "always", "mention":
			return oc.connector.Config.Messages.GroupChat.Activation
		}
	}
	return "mention"
}
