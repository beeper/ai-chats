package providers

import "github.com/beeper/ai-bridge/pkg/ai"

func InferCopilotInitiator(messages []ai.Message) string {
	if len(messages) == 0 {
		return "user"
	}
	last := messages[len(messages)-1]
	if last.Role != ai.RoleUser {
		return "agent"
	}
	return "user"
}

func HasCopilotVisionInput(messages []ai.Message) bool {
	for _, msg := range messages {
		switch msg.Role {
		case ai.RoleUser, ai.RoleToolResult:
			for _, block := range msg.Content {
				if block.Type == ai.ContentTypeImage {
					return true
				}
			}
		}
	}
	return false
}

func BuildCopilotDynamicHeaders(messages []ai.Message, hasImages bool) map[string]string {
	headers := map[string]string{
		"X-Initiator":   InferCopilotInitiator(messages),
		"Openai-Intent": "conversation-edits",
	}
	if hasImages {
		headers["Copilot-Vision-Request"] = "true"
	}
	return headers
}
