package providers

import (
	"strings"

	"github.com/beeper/ai-bridge/pkg/ai"
	"github.com/beeper/ai-bridge/pkg/ai/utils"
)

type GoogleThinkingOptions struct {
	Enabled      bool
	BudgetTokens *int
	Level        string
}

type GoogleOptions struct {
	StreamOptions ai.StreamOptions
	ToolChoice    string
	Thinking      *GoogleThinkingOptions
}

func BuildGoogleGenerateContentParams(model ai.Model, context ai.Context, options GoogleOptions) map[string]any {
	params := map[string]any{
		"model":    model.ID,
		"contents": ConvertGoogleMessages(model, context),
	}

	config := map[string]any{}
	if options.StreamOptions.Temperature != nil {
		config["temperature"] = *options.StreamOptions.Temperature
	}
	if options.StreamOptions.MaxTokens > 0 {
		config["maxOutputTokens"] = options.StreamOptions.MaxTokens
	}
	if strings.TrimSpace(context.SystemPrompt) != "" {
		config["systemInstruction"] = utils.SanitizeSurrogates(context.SystemPrompt)
	}
	if len(context.Tools) > 0 {
		config["tools"] = ConvertGoogleTools(context.Tools, false)
		if strings.TrimSpace(options.ToolChoice) != "" {
			config["toolConfig"] = map[string]any{
				"functionCallingConfig": map[string]any{
					"mode": MapGoogleToolChoice(options.ToolChoice),
				},
			}
		}
	}
	if options.Thinking != nil && options.Thinking.Enabled && model.Reasoning {
		thinkingConfig := map[string]any{
			"includeThoughts": true,
		}
		if strings.TrimSpace(options.Thinking.Level) != "" {
			thinkingConfig["thinkingLevel"] = strings.ToUpper(strings.TrimSpace(options.Thinking.Level))
		} else if options.Thinking.BudgetTokens != nil {
			thinkingConfig["thinkingBudget"] = *options.Thinking.BudgetTokens
		}
		config["thinkingConfig"] = thinkingConfig
	}
	if len(config) > 0 {
		params["config"] = config
	}
	return params
}
