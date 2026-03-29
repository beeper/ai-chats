package ai

func promptHasAudioContent(prompt PromptContext) bool {
	_ = prompt
	return false
}

func promptHasMultimodalContent(prompt PromptContext) bool {
	for _, msg := range prompt.Messages {
		for _, block := range msg.Blocks {
			if block.Type == PromptBlockImage {
				return true
			}
		}
	}
	return false
}
