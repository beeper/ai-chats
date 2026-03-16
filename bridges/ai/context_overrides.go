package ai

import (
	"context"
	"strings"
)

type contextKeyModelOverride struct{}

func withModelOverride(ctx context.Context, model string) context.Context {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return ctx
	}
	return context.WithValue(ctx, contextKeyModelOverride{}, trimmed)
}

func modelOverrideFromContext(ctx context.Context) (string, bool) {
	model := strings.TrimSpace(contextValue[string](ctx, contextKeyModelOverride{}))
	return model, model != ""
}
