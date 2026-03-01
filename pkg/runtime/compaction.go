package runtime

type CompactionInput struct {
	Messages      []string
	MaxChars      int
	ProtectedTail int
}

type CompactionResult struct {
	Messages      []string
	DroppedCount  int
	OriginalChars int
	FinalChars    int
}

func ApplyCompaction(input CompactionInput) CompactionResult {
	messages := append([]string(nil), input.Messages...)
	result := CompactionResult{Messages: messages}
	for _, msg := range messages {
		result.OriginalChars += len(msg)
	}
	if input.MaxChars <= 0 || len(messages) == 0 || result.OriginalChars <= input.MaxChars {
		result.FinalChars = result.OriginalChars
		return result
	}
	protected := input.ProtectedTail
	if protected < 0 {
		protected = 0
	}
	if protected > len(messages) {
		protected = len(messages)
	}
	cutoff := len(messages) - protected
	if cutoff < 0 {
		cutoff = 0
	}
	for result.OriginalChars > input.MaxChars && cutoff > 0 {
		dropped := messages[0]
		messages = messages[1:]
		result.OriginalChars -= len(dropped)
		result.DroppedCount++
		cutoff--
	}
	result.Messages = messages
	result.FinalChars = result.OriginalChars
	return result
}
