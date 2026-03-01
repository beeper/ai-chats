package runtime

import "github.com/openai/openai-go/v3"

type OverflowCompactionInput struct {
	Prompt              []openai.ChatCompletionMessageParamUnion
	ContextWindowTokens int
	RequestedTokens     int
	ReserveTokens       int
	ProtectedTail       int
}

type OverflowCompactionResult struct {
	Prompt   []openai.ChatCompletionMessageParamUnion
	Decision CompactionDecision
	Success  bool
}

// CompactPromptOnOverflow applies deterministic compaction + smart truncation for overflow retries.
func CompactPromptOnOverflow(input OverflowCompactionInput) OverflowCompactionResult {
	charInputs, totalChars := PromptTextPayloads(input.Prompt)
	if len(input.Prompt) <= 2 || totalChars <= 0 {
		decision := CompactionDecision{
			Applied:       false,
			DroppedCount:  0,
			OriginalChars: totalChars,
			FinalChars:    totalChars,
			Reason:        "insufficient_prompt",
		}
		return OverflowCompactionResult{
			Prompt:   input.Prompt,
			Decision: decision,
			Success:  false,
		}
	}

	protectedTail := input.ProtectedTail
	if protectedTail <= 0 {
		protectedTail = 3
	}
	reserve := input.ReserveTokens
	if reserve < 0 {
		reserve = 0
	}

	maxChars := totalChars / 2
	if input.ContextWindowTokens > 0 {
		budget := (input.ContextWindowTokens - reserve) * CharsPerTokenEstimate
		if budget > 0 && budget < maxChars {
			maxChars = budget
		}
	}
	if input.RequestedTokens > input.ContextWindowTokens && input.ContextWindowTokens > 0 {
		targetKeep := float64(input.ContextWindowTokens) / float64(input.RequestedTokens)
		targetChars := int(float64(totalChars) * targetKeep)
		if targetChars > 0 && targetChars < maxChars {
			maxChars = targetChars
		}
	}
	if maxChars <= 0 {
		maxChars = totalChars / 2
	}
	if maxChars <= 0 {
		maxChars = 1
	}

	compaction := ApplyCompaction(CompactionInput{
		Messages:      charInputs,
		MaxChars:      maxChars,
		ProtectedTail: protectedTail,
	})
	decision := compaction.Decision
	if !decision.Applied {
		return OverflowCompactionResult{
			Prompt:   input.Prompt,
			Decision: decision,
			Success:  false,
		}
	}

	ratio := 0.5
	if decision.OriginalChars > 0 && decision.FinalChars > 0 {
		keep := float64(decision.FinalChars) / float64(decision.OriginalChars)
		ratio = 1 - keep
	}
	if ratio < 0.1 {
		ratio = 0.1
	}
	if ratio > 0.85 {
		ratio = 0.85
	}

	compacted := SmartTruncatePrompt(input.Prompt, ratio)
	if len(compacted) >= len(input.Prompt) {
		compacted = SmartTruncatePrompt(input.Prompt, 0.5)
	}
	success := len(compacted) > 2 && len(compacted) < len(input.Prompt)
	return OverflowCompactionResult{
		Prompt:   compacted,
		Decision: decision,
		Success:  success,
	}
}
