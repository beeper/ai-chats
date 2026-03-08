package memory

import (
	"context"
	"slices"
	"strings"

	"github.com/openai/openai-go/v3"

	iruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
)

type PromptAugmentDeps struct {
	ShouldInjectContext   func(scope iruntime.PromptScope) bool
	ShouldBootstrap       func(scope iruntime.PromptScope) bool
	ResolveBootstrapPaths func(scope iruntime.PromptScope) []string
	MarkBootstrapped      func(ctx context.Context, scope iruntime.PromptScope)
	ReadSection           func(ctx context.Context, scope iruntime.PromptScope, path string) string
}

func AugmentPrompt(
	ctx context.Context,
	scope iruntime.PromptScope,
	prompt []openai.ChatCompletionMessageParamUnion,
	deps PromptAugmentDeps,
) []openai.ChatCompletionMessageParamUnion {
	if deps.ShouldInjectContext == nil || !deps.ShouldInjectContext(scope) {
		return prompt
	}
	if deps.ReadSection == nil {
		return prompt
	}

	sections := make([]string, 0, 3)
	if section := deps.ReadSection(ctx, scope, "MEMORY.md"); section != "" {
		sections = append(sections, section)
	} else if section := deps.ReadSection(ctx, scope, "memory.md"); section != "" {
		sections = append(sections, section)
	}

	if deps.ShouldBootstrap != nil && deps.ShouldBootstrap(scope) {
		if deps.ResolveBootstrapPaths != nil {
			for _, path := range deps.ResolveBootstrapPaths(scope) {
				if section := deps.ReadSection(ctx, scope, path); section != "" {
					sections = append(sections, section)
				}
			}
		}
		if deps.MarkBootstrapped != nil {
			deps.MarkBootstrapped(ctx, scope)
		}
	}

	if len(sections) == 0 {
		return prompt
	}
	contextText := strings.Join(sections, "\n\n")
	out := slices.Clone(prompt)
	out = append(out, openai.SystemMessage(contextText))
	return out
}
