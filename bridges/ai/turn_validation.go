package ai

import (
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
)

// IsGoogleModel returns true if the model ID looks like a Google/Gemini model.
func IsGoogleModel(modelID string) bool {
	lower := strings.ToLower(modelID)
	return strings.HasPrefix(lower, "google/") ||
		strings.HasPrefix(lower, "gemini") ||
		strings.Contains(lower, "/gemini")
}

// SanitizeGoogleTurnOrdering fixes prompt ordering for Google models:
//   - Merges consecutive user messages
//   - Merges consecutive assistant messages
//   - Prepends a synthetic user turn if history starts with an assistant message
func SanitizeGoogleTurnOrdering(prompt []openai.ChatCompletionMessageParamUnion) []openai.ChatCompletionMessageParamUnion {
	if len(prompt) == 0 {
		return prompt
	}

	// Separate system messages (keep at front) from conversation messages
	var system []openai.ChatCompletionMessageParamUnion
	var conversation []openai.ChatCompletionMessageParamUnion
	for _, msg := range prompt {
		if chatMessageRole(msg) == "system" {
			system = append(system, msg)
		} else {
			conversation = append(conversation, msg)
		}
	}

	if len(conversation) == 0 {
		return prompt
	}

	// Merge consecutive same-role messages
	merged := mergeConsecutiveSameRole(conversation)

	// If the first non-system message is assistant, prepend a synthetic user turn
	if len(merged) > 0 && chatMessageRole(merged[0]) == "assistant" {
		merged = append([]openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("(continued from previous session)"),
		}, merged...)
	}

	return append(system, merged...)
}

// mergeConsecutiveSameRole combines adjacent messages with the same role.
// For user messages, it preserves multimodal content parts (images, etc.)
// by collecting all parts from the run into a single OfArrayOfContentParts message.
// For assistant messages, it concatenates text bodies with double newlines.
func mergeConsecutiveSameRole(msgs []openai.ChatCompletionMessageParamUnion) []openai.ChatCompletionMessageParamUnion {
	if len(msgs) <= 1 {
		return msgs
	}

	var result []openai.ChatCompletionMessageParamUnion
	i := 0
	for i < len(msgs) {
		role := chatMessageRole(msgs[i])
		j := i + 1
		for j < len(msgs) && chatMessageRole(msgs[j]) == role {
			j++
		}

		// Single message, no merging needed — keep as-is.
		if j == i+1 {
			result = append(result, msgs[i])
			i = j
			continue
		}

		// Multiple consecutive messages with the same role — merge them.
		run := msgs[i:j]
		switch role {
		case "user":
			result = append(result, mergeUserMessages(run))
		case "assistant":
			// Assistant messages are always text-only (images go in synthetic user messages).
			var body string
			for _, m := range run {
				nextBody := chatMessageBody(m)
				if nextBody != "" {
					if body != "" {
						body += "\n\n"
					}
					body += nextBody
				}
			}
			result = append(result, openai.AssistantMessage(body))
		default:
			// For other roles, concatenate text.
			var body string
			for _, m := range run {
				nextBody := chatMessageBody(m)
				if nextBody != "" {
					if body != "" {
						body += "\n\n"
					}
					body += nextBody
				}
			}
			result = append(result, openai.UserMessage(body))
		}
		i = j
	}
	return result
}

// mergeUserMessages merges a run of consecutive user messages into one,
// preserving multimodal content parts (OfArrayOfContentParts) if any message has them.
func mergeUserMessages(run []openai.ChatCompletionMessageParamUnion) openai.ChatCompletionMessageParamUnion {
	// Check if any message in the run has multimodal parts.
	hasMultimodal := false
	for _, m := range run {
		if m.OfUser != nil && len(m.OfUser.Content.OfArrayOfContentParts) > 0 {
			hasMultimodal = true
			break
		}
	}

	// If no multimodal content, do the simple text merge.
	if !hasMultimodal {
		var body string
		for _, m := range run {
			nextBody := chatMessageBody(m)
			if nextBody != "" {
				if body != "" {
					body += "\n\n"
				}
				body += nextBody
			}
		}
		return openai.UserMessage(body)
	}

	// Collect all content parts, converting plain-text messages to text parts.
	var allParts []openai.ChatCompletionContentPartUnionParam
	for idx, m := range run {
		if m.OfUser == nil {
			continue
		}
		if len(m.OfUser.Content.OfArrayOfContentParts) > 0 {
			// Add a separator text part between merged messages (except the first).
			if idx > 0 && len(allParts) > 0 {
				allParts = append(allParts, openai.ChatCompletionContentPartUnionParam{
					OfText: &openai.ChatCompletionContentPartTextParam{Text: "\n\n"},
				})
			}
			allParts = append(allParts, m.OfUser.Content.OfArrayOfContentParts...)
		} else if m.OfUser.Content.OfString.Value != "" {
			if len(allParts) > 0 {
				allParts = append(allParts, openai.ChatCompletionContentPartUnionParam{
					OfText: &openai.ChatCompletionContentPartTextParam{Text: "\n\n"},
				})
			}
			allParts = append(allParts, openai.ChatCompletionContentPartUnionParam{
				OfText: &openai.ChatCompletionContentPartTextParam{Text: m.OfUser.Content.OfString.Value},
			})
		}
	}

	return openai.ChatCompletionMessageParamUnion{
		OfUser: &openai.ChatCompletionUserMessageParam{
			Content: openai.ChatCompletionUserMessageParamContentUnion{
				OfArrayOfContentParts: allParts,
			},
		},
	}
}

// chatMessageRole extracts the role string from a ChatCompletionMessageParamUnion.
// GetRole() returns empty strings at construction time (constant types marshal lazily),
// so we check which Of* field is populated instead.
func chatMessageRole(msg openai.ChatCompletionMessageParamUnion) string {
	if !param.IsOmitted(msg.OfSystem) {
		return "system"
	}
	if !param.IsOmitted(msg.OfUser) {
		return "user"
	}
	if !param.IsOmitted(msg.OfAssistant) {
		return "assistant"
	}
	if !param.IsOmitted(msg.OfTool) {
		return "tool"
	}
	if !param.IsOmitted(msg.OfDeveloper) {
		return "developer"
	}
	return "user"
}

// chatMessageBody extracts the text body from a ChatCompletionMessageParamUnion.
func chatMessageBody(msg openai.ChatCompletionMessageParamUnion) string {
	c := msg.GetContent()
	if s, ok := c.AsAny().(*string); ok && s != nil {
		return *s
	}
	return ""
}
