package connector

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

// ValidateGeminiTurns checks whether the prompt satisfies Google's strict
// user→assistant alternation requirement. Returns true if the prompt is valid.
func ValidateGeminiTurns(prompt []openai.ChatCompletionMessageParamUnion) bool {
	lastRole := ""
	for _, msg := range prompt {
		role := chatMessageRole(msg)
		if role == "system" {
			continue
		}
		if role == lastRole && (role == "user" || role == "assistant") {
			return false
		}
		lastRole = role
	}
	return true
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

// mergeConsecutiveSameRole combines adjacent messages with the same role
// by concatenating their text content with double newlines.
func mergeConsecutiveSameRole(msgs []openai.ChatCompletionMessageParamUnion) []openai.ChatCompletionMessageParamUnion {
	if len(msgs) <= 1 {
		return msgs
	}

	var result []openai.ChatCompletionMessageParamUnion
	i := 0
	for i < len(msgs) {
		role := chatMessageRole(msgs[i])
		body := chatMessageBody(msgs[i])
		j := i + 1
		for j < len(msgs) && chatMessageRole(msgs[j]) == role {
			nextBody := chatMessageBody(msgs[j])
			if nextBody != "" {
				if body != "" {
					body += "\n\n"
				}
				body += nextBody
			}
			j++
		}
		switch role {
		case "assistant":
			result = append(result, openai.AssistantMessage(body))
		default:
			result = append(result, openai.UserMessage(body))
		}
		i = j
	}
	return result
}

// chatMessageRole extracts the role string from a ChatCompletionMessageParamUnion.
func chatMessageRole(msg openai.ChatCompletionMessageParamUnion) string {
	if r := msg.GetRole(); r != nil {
		return *r
	}
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
	if c.OfString != nil {
		return *c.OfString
	}
	return ""
}
