package providers

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	"google.golang.org/genai"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func streamGoogleGenerativeAI(model ai.Model, c ai.Context, options *ai.StreamOptions) *ai.AssistantMessageEventStream {
	googleOptions := GoogleOptions{}
	if options != nil {
		googleOptions.StreamOptions = *options
	}
	return streamGoogleWithBackend(model, c, googleOptions, genai.BackendGeminiAPI)
}

func streamSimpleGoogleGenerativeAI(model ai.Model, c ai.Context, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	base := BuildBaseOptions(model, options, "")
	return streamGoogleWithBackend(model, c, buildGoogleOptionsFromSimple(model, base, options), genai.BackendGeminiAPI)
}

func streamGoogleVertex(model ai.Model, c ai.Context, options *ai.StreamOptions) *ai.AssistantMessageEventStream {
	googleOptions := GoogleOptions{}
	if options != nil {
		googleOptions.StreamOptions = *options
	}
	return streamGoogleWithBackend(model, c, googleOptions, genai.BackendVertexAI)
}

func streamSimpleGoogleVertex(model ai.Model, c ai.Context, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	base := BuildBaseOptions(model, options, "")
	return streamGoogleWithBackend(model, c, buildGoogleOptionsFromSimple(model, base, options), genai.BackendVertexAI)
}

func buildGoogleOptionsFromSimple(model ai.Model, base ai.StreamOptions, options *ai.SimpleStreamOptions) GoogleOptions {
	out := GoogleOptions{StreamOptions: base}
	if options == nil || options.Reasoning == "" || !model.Reasoning {
		return out
	}
	level := strings.ToLower(strings.TrimSpace(string(options.Reasoning)))
	if level == "xhigh" {
		level = "high"
	}
	adjustedMaxTokens, thinkingBudget := AdjustMaxTokensForThinking(
		base.MaxTokens,
		model.MaxTokens,
		options.Reasoning,
		options.ThinkingBudgets,
	)
	out.StreamOptions.MaxTokens = adjustedMaxTokens
	out.Thinking = &GoogleThinkingOptions{
		Enabled: true,
		Level:   level,
	}
	if thinkingBudget > 0 {
		out.Thinking.BudgetTokens = &thinkingBudget
	}
	return out
}

func streamGoogleWithBackend(
	model ai.Model,
	c ai.Context,
	options GoogleOptions,
	backend genai.Backend,
) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream(128)
	go func() {
		runCtx := options.StreamOptions.Ctx
		if runCtx == nil {
			runCtx = context.Background()
		}

		client, err := newGoogleClient(runCtx, backend, options.StreamOptions.APIKey)
		if err != nil {
			pushProviderError(stream, model, err.Error())
			return
		}

		payload := BuildGoogleGenerateContentParams(model, c, options)
		if options.StreamOptions.OnPayload != nil {
			options.StreamOptions.OnPayload(payload)
		}

		contents := convertGoogleContextToGenAIContents(model, c)
		config := buildGenAIContentConfig(model, c, options)
		textBuilder := strings.Builder{}
		thinkingBuilder := strings.Builder{}
		toolCalls := make([]ai.ContentBlock, 0, 2)
		usage := ai.Usage{}
		stopReason := ai.StopReasonStop

		for result, err := range client.Models.GenerateContentStream(runCtx, model.ID, contents, config) {
			if err != nil {
				pushProviderError(stream, model, err.Error())
				return
			}
			if result == nil {
				continue
			}
			if result.UsageMetadata != nil {
				usage = ai.Usage{
					Input:       int(result.UsageMetadata.PromptTokenCount),
					Output:      int(result.UsageMetadata.CandidatesTokenCount),
					TotalTokens: int(result.UsageMetadata.TotalTokenCount),
				}
			}

			for _, candidate := range result.Candidates {
				if candidate == nil {
					continue
				}
				if candidate.FinishReason != "" {
					stopReason = MapGoogleStopReason(string(candidate.FinishReason))
				}
				if candidate.Content == nil {
					continue
				}
				for _, part := range candidate.Content.Parts {
					if part == nil {
						continue
					}
					if part.FunctionCall != nil {
						toolCall := ai.ContentBlock{
							Type:      ai.ContentTypeToolCall,
							ID:        strings.TrimSpace(part.FunctionCall.ID),
							Name:      strings.TrimSpace(part.FunctionCall.Name),
							Arguments: part.FunctionCall.Args,
						}
						toolCalls = append(toolCalls, toolCall)
						stream.Push(ai.AssistantMessageEvent{
							Type:     ai.EventToolCallEnd,
							ToolCall: &toolCall,
						})
					}
					if strings.TrimSpace(part.Text) != "" {
						if part.Thought {
							thinkingBuilder.WriteString(part.Text)
							stream.Push(ai.AssistantMessageEvent{
								Type:  ai.EventThinkingDelta,
								Delta: part.Text,
							})
						} else {
							textBuilder.WriteString(part.Text)
							stream.Push(ai.AssistantMessageEvent{
								Type:  ai.EventTextDelta,
								Delta: part.Text,
							})
						}
					}
				}
			}
		}

		usage.Cost = ai.CalculateCost(model, usage)
		assistantMessage := ai.Message{
			Role:       ai.RoleAssistant,
			API:        model.API,
			Provider:   model.Provider,
			Model:      model.ID,
			Usage:      usage,
			StopReason: stopReason,
			Timestamp:  time.Now().UnixMilli(),
		}
		if thinking := strings.TrimSpace(thinkingBuilder.String()); thinking != "" {
			assistantMessage.Content = append(assistantMessage.Content, ai.ContentBlock{
				Type:     ai.ContentTypeThinking,
				Thinking: thinking,
			})
		}
		if text := strings.TrimSpace(textBuilder.String()); text != "" {
			assistantMessage.Content = append(assistantMessage.Content, ai.ContentBlock{
				Type: ai.ContentTypeText,
				Text: text,
			})
		}
		if len(toolCalls) > 0 {
			assistantMessage.Content = append(assistantMessage.Content, toolCalls...)
		}
		if len(toolCalls) > 0 && assistantMessage.StopReason == ai.StopReasonStop {
			assistantMessage.StopReason = ai.StopReasonToolUse
		}
		stream.Push(ai.AssistantMessageEvent{
			Type:    ai.EventDone,
			Message: assistantMessage,
			Reason:  assistantMessage.StopReason,
		})
	}()
	return stream
}

func newGoogleClient(ctx context.Context, backend genai.Backend, apiKey string) (*genai.Client, error) {
	switch backend {
	case genai.BackendGeminiAPI:
		if strings.TrimSpace(apiKey) == "" {
			apiKey = ai.GetEnvAPIKey("google")
		}
		if strings.TrimSpace(apiKey) == "" {
			return nil, errProvider("missing API key for Google Generative AI runtime")
		}
		return genai.NewClient(ctx, &genai.ClientConfig{
			APIKey:  apiKey,
			Backend: genai.BackendGeminiAPI,
		})
	case genai.BackendVertexAI:
		project, err := ResolveGoogleVertexProject(nil)
		if err != nil {
			return nil, err
		}
		location, err := ResolveGoogleVertexLocation(nil)
		if err != nil {
			return nil, err
		}
		if ai.GetEnvAPIKey("google-vertex") == "" {
			return nil, errProvider("missing ADC credentials for Google Vertex runtime")
		}
		return genai.NewClient(ctx, &genai.ClientConfig{
			Project:  project,
			Location: location,
			Backend:  genai.BackendVertexAI,
		})
	default:
		return nil, errProvider("unsupported Google backend")
	}
}

func convertGoogleContextToGenAIContents(model ai.Model, c ai.Context) []*genai.Content {
	googleMessages := ConvertGoogleMessages(model, c)
	out := make([]*genai.Content, 0, len(googleMessages))
	for _, msg := range googleMessages {
		parts := make([]*genai.Part, 0, len(msg.Parts))
		for _, part := range msg.Parts {
			switch {
			case strings.TrimSpace(part.Text) != "":
				p := &genai.Part{
					Text:    part.Text,
					Thought: part.Thought,
				}
				if strings.TrimSpace(part.ThoughtSignature) != "" {
					p.ThoughtSignature = []byte(part.ThoughtSignature)
				}
				parts = append(parts, p)
			case part.FunctionCall != nil:
				parts = append(parts, genai.NewPartFromFunctionCall(part.FunctionCall.Name, part.FunctionCall.Args))
			case part.FunctionResponse != nil:
				parts = append(parts, genai.NewPartFromFunctionResponse(part.FunctionResponse.Name, part.FunctionResponse.Response))
			case part.InlineData != nil:
				if data, ok := decodeBase64(part.InlineData.Data); ok {
					parts = append(parts, genai.NewPartFromBytes(data, part.InlineData.MimeType))
				}
			}
		}
		if len(parts) == 0 {
			continue
		}
		out = append(out, &genai.Content{
			Role:  msg.Role,
			Parts: parts,
		})
	}
	return out
}

func buildGenAIContentConfig(model ai.Model, c ai.Context, options GoogleOptions) *genai.GenerateContentConfig {
	config := &genai.GenerateContentConfig{}
	if options.StreamOptions.Temperature != nil {
		temp := float32(*options.StreamOptions.Temperature)
		config.Temperature = &temp
	}
	if options.StreamOptions.MaxTokens > 0 {
		config.MaxOutputTokens = int32(options.StreamOptions.MaxTokens)
	}
	if strings.TrimSpace(c.SystemPrompt) != "" {
		config.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{{Text: c.SystemPrompt}},
		}
	}
	if len(c.Tools) > 0 {
		config.Tools = convertGoogleToolsToGenAI(c.Tools)
		if strings.TrimSpace(options.ToolChoice) != "" {
			config.ToolConfig = &genai.ToolConfig{
				FunctionCallingConfig: &genai.FunctionCallingConfig{
					Mode: mapGoogleToolChoiceToGenAI(options.ToolChoice),
				},
			}
		}
	}
	if options.Thinking != nil && options.Thinking.Enabled && model.Reasoning {
		thinking := &genai.ThinkingConfig{
			IncludeThoughts: true,
		}
		if options.Thinking.BudgetTokens != nil && *options.Thinking.BudgetTokens > 0 {
			value := int32(*options.Thinking.BudgetTokens)
			thinking.ThinkingBudget = &value
		}
		if level := mapThinkingLevelToGenAI(options.Thinking.Level); level != "" {
			thinking.ThinkingLevel = level
		}
		config.ThinkingConfig = thinking
	}
	return config
}

func convertGoogleToolsToGenAI(tools []ai.Tool) []*genai.Tool {
	out := make([]*genai.Tool, 0, len(tools))
	if len(tools) == 0 {
		return out
	}
	declarations := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, tool := range tools {
		declarations = append(declarations, &genai.FunctionDeclaration{
			Name:                 tool.Name,
			Description:          tool.Description,
			ParametersJsonSchema: tool.Parameters,
		})
	}
	out = append(out, &genai.Tool{
		FunctionDeclarations: declarations,
	})
	return out
}

func mapGoogleToolChoiceToGenAI(choice string) genai.FunctionCallingConfigMode {
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "none":
		return genai.FunctionCallingConfigModeNone
	case "any":
		return genai.FunctionCallingConfigModeAny
	default:
		return genai.FunctionCallingConfigModeAuto
	}
}

func mapThinkingLevelToGenAI(level string) genai.ThinkingLevel {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "minimal":
		return genai.ThinkingLevelMinimal
	case "low":
		return genai.ThinkingLevelLow
	case "medium":
		return genai.ThinkingLevelMedium
	case "high", "xhigh":
		return genai.ThinkingLevelHigh
	default:
		return ""
	}
}

func decodeBase64(value string) ([]byte, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false
	}
	if data, err := base64.StdEncoding.DecodeString(value); err == nil {
		return data, true
	}
	if data, err := base64.RawStdEncoding.DecodeString(value); err == nil {
		return data, true
	}
	return nil, false
}

func errProvider(message string) error {
	return &providerError{message: strings.TrimSpace(message)}
}

type providerError struct {
	message string
}

func (e *providerError) Error() string {
	return e.message
}
