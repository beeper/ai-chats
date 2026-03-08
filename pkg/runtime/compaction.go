package runtime

import "slices"

// CompactionInput describes text-level compaction parameters.
type CompactionInput struct {
	Messages      []string
	MaxChars      int
	ProtectedTail int
}

// CompactionResult holds the outcome of a text-level compaction pass.
type CompactionResult struct {
	Messages []string
	Decision CompactionDecision
}

// ApplyCompaction drops oldest messages until the total character count fits within budget,
// while respecting the protected tail window.
func ApplyCompaction(input CompactionInput) CompactionResult {
	messages := slices.Clone(input.Messages)
	originalChars := 0
	for _, msg := range messages {
		originalChars += len(msg)
	}

	if input.MaxChars <= 0 || len(messages) == 0 || originalChars <= input.MaxChars {
		return CompactionResult{
			Messages: messages,
			Decision: CompactionDecision{
				OriginalChars: originalChars,
				FinalChars:    originalChars,
				Reason:        "within_budget",
			},
		}
	}

	protected := input.ProtectedTail
	if protected < 0 {
		protected = 0
	}
	if protected > len(messages) {
		protected = len(messages)
	}
	cutoff := len(messages) - protected

	currentChars := originalChars
	droppedCount := 0
	for currentChars > input.MaxChars && cutoff > 0 && len(messages) > 0 {
		currentChars -= len(messages[0])
		messages = messages[1:]
		droppedCount++
		cutoff--
	}

	reason := "drop_oldest"
	if droppedCount == 0 {
		reason = "protected_tail_prevented_drop"
	} else if currentChars > input.MaxChars {
		reason = "budget_exceeded_after_drop"
	}

	return CompactionResult{
		Messages: messages,
		Decision: CompactionDecision{
			Applied:       droppedCount > 0,
			DroppedCount:  droppedCount,
			OriginalChars: originalChars,
			FinalChars:    currentChars,
			Reason:        reason,
		},
	}
}
