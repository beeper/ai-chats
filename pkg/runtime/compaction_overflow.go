package runtime

import (
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
)

type OverflowCompactionInput struct {
	Prompt              []openai.ChatCompletionMessageParamUnion
	ContextWindowTokens int
	RequestedTokens     int
	CurrentPromptTokens int
	ReserveTokens       int
	KeepRecentTokens    int
	CompactionMode      string
	Summarization       bool
	MaxSummaryTokens    int
	RefreshPrompt       string
	MaxHistoryShare     float64
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
	mode := strings.ToLower(strings.TrimSpace(input.CompactionMode))
	if mode == "" {
		mode = "safeguard"
	}
	keepRecent := input.KeepRecentTokens
	if keepRecent < 0 {
		keepRecent = 0
	}
	maxHistoryShare := input.MaxHistoryShare
	if maxHistoryShare <= 0 || maxHistoryShare >= 1 {
		maxHistoryShare = 0.5
	}
	currentPromptTokens := input.CurrentPromptTokens
	if currentPromptTokens <= 0 {
		currentPromptTokens = totalChars / CharsPerTokenEstimate
		if currentPromptTokens <= 0 {
			currentPromptTokens = len(input.Prompt) * 4
		}
	}
	maxChars := totalChars
	if input.ContextWindowTokens > 0 {
		budgetAfterReserve := (input.ContextWindowTokens - reserve) * CharsPerTokenEstimate
		if budgetAfterReserve > 0 && budgetAfterReserve < maxChars {
			maxChars = budgetAfterReserve
		}
		historyShareBudget := int(float64(input.ContextWindowTokens*CharsPerTokenEstimate) * maxHistoryShare)
		if historyShareBudget > 0 && historyShareBudget < maxChars {
			maxChars = historyShareBudget
		}
	}
	if mode == "safeguard" && keepRecent > 0 {
		avgChars := 1
		if len(charInputs) > 0 {
			avgChars = totalChars / len(charInputs)
			if avgChars <= 0 {
				avgChars = 1
			}
		}
		keepRecentChars := keepRecent * CharsPerTokenEstimate
		if keepRecentChars > 0 {
			derivedTail := keepRecentChars / avgChars
			if derivedTail > protectedTail {
				protectedTail = derivedTail
			}
			// Safeguard mode avoids collapsing recent context too aggressively.
			if maxChars > 0 && maxChars < keepRecentChars {
				maxChars = keepRecentChars
			}
		}
	}
	if input.RequestedTokens > input.ContextWindowTokens && input.ContextWindowTokens > 0 {
		targetKeep := float64(input.ContextWindowTokens) / float64(input.RequestedTokens)
		targetChars := int(float64(totalChars) * targetKeep)
		if targetChars > 0 && targetChars < maxChars {
			maxChars = targetChars
		}
	}
	if maxChars >= totalChars {
		maxChars = int(float64(totalChars) * 0.85)
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

	targetPromptTokens := currentPromptTokens
	if input.ContextWindowTokens > 0 {
		targetPromptTokens = input.ContextWindowTokens - reserve
		shareTokens := int(float64(input.ContextWindowTokens) * maxHistoryShare)
		if shareTokens > 0 && shareTokens < targetPromptTokens {
			targetPromptTokens = shareTokens
		}
		if mode == "safeguard" && keepRecent > 0 && targetPromptTokens < keepRecent {
			targetPromptTokens = keepRecent
		}
		if targetPromptTokens <= 0 {
			targetPromptTokens = currentPromptTokens / 2
		}
	}
	if targetPromptTokens <= 0 {
		targetPromptTokens = currentPromptTokens / 2
	}
	if targetPromptTokens <= 0 {
		targetPromptTokens = 1
	}
	ratio := 0.5
	if currentPromptTokens > 0 {
		keepFraction := float64(targetPromptTokens) / float64(currentPromptTokens)
		if keepFraction < 0.1 {
			keepFraction = 0.1
		}
		if keepFraction > 0.95 {
			keepFraction = 0.95
		}
		ratio = 1 - keepFraction
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
	if input.Summarization {
		maxSummaryTokens := input.MaxSummaryTokens
		if maxSummaryTokens <= 0 {
			maxSummaryTokens = 500
		}
		compacted = injectCompactionSummary(compacted, input.Prompt, decision.DroppedCount, maxSummaryTokens)
	}
	if strings.TrimSpace(input.RefreshPrompt) != "" {
		compacted = injectCompactionRefreshPrompt(compacted, input.RefreshPrompt)
	}
	success := len(compacted) > 2 && decision.Applied && decision.FinalChars < decision.OriginalChars
	return OverflowCompactionResult{
		Prompt:   compacted,
		Decision: decision,
		Success:  success,
	}
}

func injectCompactionRefreshPrompt(
	prompt []openai.ChatCompletionMessageParamUnion,
	refreshPrompt string,
) []openai.ChatCompletionMessageParamUnion {
	if len(prompt) == 0 {
		return prompt
	}
	insertAt := 0
	for insertAt < len(prompt) {
		msg := prompt[insertAt]
		if msg.OfSystem != nil || msg.OfDeveloper != nil {
			insertAt++
			continue
		}
		break
	}
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(prompt)+1)
	out = append(out, prompt[:insertAt]...)
	out = append(out, openai.SystemMessage(strings.TrimSpace(refreshPrompt)))
	out = append(out, prompt[insertAt:]...)
	return out
}

func injectCompactionSummary(
	compacted []openai.ChatCompletionMessageParamUnion,
	original []openai.ChatCompletionMessageParamUnion,
	droppedCount int,
	maxSummaryTokens int,
) []openai.ChatCompletionMessageParamUnion {
	if len(compacted) == 0 || len(original) == 0 {
		return compacted
	}
	if droppedCount <= 0 {
		droppedCount = len(original) - len(compacted)
	}
	if droppedCount <= 0 {
		return compacted
	}
	if droppedCount > len(original) {
		droppedCount = len(original)
	}
	summary := buildCompactionSummaryText(original[:droppedCount], maxSummaryTokens)
	if summary == "" {
		return compacted
	}
	insertAt := 0
	for insertAt < len(compacted) {
		msg := compacted[insertAt]
		if msg.OfSystem != nil || msg.OfDeveloper != nil {
			insertAt++
			continue
		}
		break
	}
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(compacted)+1)
	out = append(out, compacted[:insertAt]...)
	out = append(out, openai.SystemMessage(summary))
	out = append(out, compacted[insertAt:]...)
	return out
}

func buildCompactionSummaryText(
	dropped []openai.ChatCompletionMessageParamUnion,
	maxSummaryTokens int,
) string {
	if len(dropped) == 0 {
		return ""
	}
	if maxSummaryTokens <= 0 {
		maxSummaryTokens = 500
	}
	maxChars := maxSummaryTokens * CharsPerTokenEstimate
	if maxChars < 240 {
		maxChars = 240
	}
	var b strings.Builder
	b.WriteString("[Compaction summary of earlier context]\n")
	for _, msg := range dropped {
		text, role := ExtractMessageContent(msg)
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		text = strings.ReplaceAll(text, "\n", " ")
		if len(text) > 220 {
			text = text[:220] + "..."
		}
		line := fmt.Sprintf("- %s: %s\n", role, text)
		if b.Len()+len(line) > maxChars {
			break
		}
		b.WriteString(line)
	}
	result := strings.TrimSpace(b.String())
	if result == "[Compaction summary of earlier context]" {
		return ""
	}
	return result
}
