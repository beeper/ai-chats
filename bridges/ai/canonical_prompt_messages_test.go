package ai

func setCanonicalTurnDataFromPromptMessages(meta *MessageMetadata, messages []PromptMessage) {
	if meta == nil || len(messages) == 0 {
		return
	}
	if turnData, ok := buildUserTurnDataFromPromptBlocks(messages[0].Blocks); ok {
		meta.CanonicalTurnData = turnData.ToMap()
	} else {
		meta.CanonicalTurnData = nil
	}
}
