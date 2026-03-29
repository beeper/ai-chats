package ai

import (
	"context"
	"errors"
	"fmt"

	"github.com/openai/openai-go/v3"
)

func (o *OpenAIProvider) generateChatCompletions(ctx context.Context, params GenerateParams) (*GenerateResponse, error) {
	chatMessages := promptContextToChatCompletionMessages(params.Context, isOpenRouterBaseURL(o.baseURL))
	if len(chatMessages) == 0 {
		return nil, errors.New("no chat messages for completion")
	}

	req := openai.ChatCompletionNewParams{
		Model:    params.Model,
		Messages: chatMessages,
	}
	if params.MaxCompletionTokens > 0 {
		req.MaxCompletionTokens = openai.Int(int64(params.MaxCompletionTokens))
	}
	if params.Temperature != nil {
		req.Temperature = openai.Float(*params.Temperature)
	}
	if len(params.Context.Tools) > 0 {
		req.Tools = ToOpenAIChatTools(params.Context.Tools, resolveToolStrictMode(isOpenRouterBaseURL(o.baseURL)), &o.log)
		req.Tools = dedupeChatToolParams(req.Tools)
	}

	resp, err := o.client.Chat.Completions.New(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("OpenAI chat completion failed: %w", err)
	}

	var content string
	var finishReason string
	if len(resp.Choices) > 0 {
		content = resp.Choices[0].Message.Content
		finishReason = resp.Choices[0].FinishReason
	}

	return &GenerateResponse{
		Content:      content,
		FinishReason: finishReason,
		Usage: UsageInfo{
			PromptTokens:     int(resp.Usage.PromptTokens),
			CompletionTokens: int(resp.Usage.CompletionTokens),
			TotalTokens:      int(resp.Usage.TotalTokens),
			ReasoningTokens:  int(resp.Usage.CompletionTokensDetails.ReasoningTokens),
		},
	}, nil
}
