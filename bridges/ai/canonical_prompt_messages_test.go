package ai

func setCanonicalTurnDataFromPromptMessages(meta *MessageMetadata, messages []PromptMessage) {
	if meta == nil || len(messages) == 0 {
		return
	}
	if turnData, ok := turnDataFromUserPromptMessages(messages); ok {
		meta.CanonicalTurnData = turnData.ToMap()
	} else {
		meta.CanonicalTurnData = nil
	}
}
