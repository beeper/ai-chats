package ai

func setCanonicalTurnDataFromPromptMessages(meta *MessageMetadata, messages []PromptMessage) {
	if meta == nil || len(messages) == 0 {
		return
	}
	if messages[0].Role != PromptRoleUser {
		meta.CanonicalTurnData = nil
		return
	}
	if _, turnData, ok := buildUserPromptTurn(messages[0].Blocks); ok {
		meta.CanonicalTurnData = turnData.ToMap()
	} else {
		meta.CanonicalTurnData = nil
	}
}
