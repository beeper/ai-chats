package connector

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	aipkg "github.com/beeper/ai-bridge/pkg/ai"
	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

type streamingRuntimePath string

const (
	streamingRuntimePkgAI           streamingRuntimePath = "pkg_ai"
	streamingRuntimeChatCompletions streamingRuntimePath = "chat_completions"
	streamingRuntimeResponses       streamingRuntimePath = "responses"
)

func pkgAIRuntimeEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("PI_USE_PKG_AI_RUNTIME")))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func chooseStreamingRuntimePath(hasAudio bool, modelAPI ModelAPI, preferPkgAI bool) streamingRuntimePath {
	if hasAudio {
		return streamingRuntimeChatCompletions
	}
	if preferPkgAI {
		return streamingRuntimePkgAI
	}
	if modelAPI == ModelAPIChatCompletions {
		return streamingRuntimeChatCompletions
	}
	return streamingRuntimeResponses
}

func (oc *AIClient) streamWithPkgAIBridge(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt []openai.ChatCompletionMessageParamUnion,
) (bool, *ContextLengthError, error) {
	aiContext := buildPkgAIContext(oc.effectivePrompt(meta), prompt)
	oc.loggerForContext(ctx).Debug().
		Int("prompt_messages", len(prompt)).
		Int("ai_messages", len(aiContext.Messages)).
		Msg("pkg/ai runtime bridge flag enabled; prepared adapter context and delegating to existing runtime path")
	switch oc.resolveModelAPI(meta) {
	case ModelAPIChatCompletions:
		return oc.streamChatCompletions(ctx, evt, portal, meta, prompt)
	default:
		return oc.streamingResponseWithToolSchemaFallback(ctx, evt, portal, meta, prompt)
	}
}

func buildPkgAIContext(systemPrompt string, prompt []openai.ChatCompletionMessageParamUnion) aipkg.Context {
	unified := chatPromptToUnifiedMessages(prompt)
	return toAIContext(systemPrompt, unified, nil)
}

func chatPromptToUnifiedMessages(prompt []openai.ChatCompletionMessageParamUnion) []UnifiedMessage {
	out := make([]UnifiedMessage, 0, len(prompt))
	now := time.Now().UnixMilli()

	for _, msg := range prompt {
		switch {
		case msg.OfUser != nil:
			parts := make([]ContentPart, 0, 2)
			userText := strings.TrimSpace(airuntime.ExtractUserContent(msg.OfUser.Content))
			if userText != "" {
				parts = append(parts, ContentPart{Type: ContentTypeText, Text: userText})
			}
			for _, part := range msg.OfUser.Content.OfArrayOfContentParts {
				if part.OfImageURL != nil && strings.TrimSpace(part.OfImageURL.ImageURL.URL) != "" {
					parts = append(parts, ContentPart{
						Type:     ContentTypeImage,
						ImageURL: strings.TrimSpace(part.OfImageURL.ImageURL.URL),
					})
				}
			}
			if len(parts) == 0 {
				continue
			}
			out = append(out, UnifiedMessage{
				Role:    RoleUser,
				Content: parts,
			})
		case msg.OfAssistant != nil:
			parts := make([]ContentPart, 0, 1)
			assistantText := strings.TrimSpace(airuntime.ExtractAssistantContent(msg.OfAssistant.Content))
			if assistantText != "" {
				parts = append(parts, ContentPart{Type: ContentTypeText, Text: assistantText})
			}
			toolCalls := make([]ToolCallResult, 0, len(msg.OfAssistant.ToolCalls))
			for _, toolCall := range msg.OfAssistant.ToolCalls {
				if toolCall.OfFunction == nil {
					continue
				}
				name := strings.TrimSpace(toolCall.OfFunction.Function.Name)
				if name == "" {
					continue
				}
				toolCalls = append(toolCalls, ToolCallResult{
					ID:        strings.TrimSpace(toolCall.OfFunction.ID),
					Name:      name,
					Arguments: strings.TrimSpace(toolCall.OfFunction.Function.Arguments),
				})
			}
			if len(parts) == 0 && len(toolCalls) == 0 {
				continue
			}
			out = append(out, UnifiedMessage{
				Role:      RoleAssistant,
				Content:   parts,
				ToolCalls: toolCalls,
			})
		case msg.OfTool != nil:
			toolText := strings.TrimSpace(airuntime.ExtractToolContent(msg.OfTool.Content))
			parts := []ContentPart{}
			if toolText != "" {
				parts = append(parts, ContentPart{Type: ContentTypeText, Text: toolText})
			}
			out = append(out, UnifiedMessage{
				Role:       RoleTool,
				ToolCallID: strings.TrimSpace(msg.OfTool.ToolCallID),
				Content:    parts,
			})
		case msg.OfSystem != nil || msg.OfDeveloper != nil:
			// System/developer content is carried separately via systemPrompt in buildPkgAIContext.
			continue
		default:
			content, role := airuntime.ExtractMessageContent(msg)
			content = strings.TrimSpace(content)
			if content == "" {
				continue
			}
			switch role {
			case "user":
				out = append(out, UnifiedMessage{Role: RoleUser, Content: []ContentPart{{Type: ContentTypeText, Text: content}}})
			case "assistant":
				out = append(out, UnifiedMessage{Role: RoleAssistant, Content: []ContentPart{{Type: ContentTypeText, Text: content}}})
			case "tool":
				out = append(out, UnifiedMessage{
					Role:       RoleTool,
					Content:    []ContentPart{{Type: ContentTypeText, Text: content}},
					ToolCallID: "tool_" + strconv.FormatInt(now, 10),
				})
			}
		}
	}
	return out
}
