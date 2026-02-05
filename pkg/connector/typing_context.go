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
	if ctx == nil {
		return nil
	}
	if val := ctx.Value(typingContextKey{}); val != nil {
		if typing, ok := val.(*TypingContext); ok {
			return typing
		}
	}
	return nil
}
