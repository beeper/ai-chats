package connector

import (
	"context"
)

type TypingContext struct {
	IsGroup      bool
	WasMentioned bool
}

type typingContextKey struct{}

func WithTypingContext(ctx context.Context, typing *TypingContext) context.Context {
	if ctx == nil || typing == nil {
		return ctx
	}
	return context.WithValue(ctx, typingContextKey{}, typing)
}

func typingContextFromContext(ctx context.Context) *TypingContext {
	return contextValue[*TypingContext](ctx, typingContextKey{})
}
