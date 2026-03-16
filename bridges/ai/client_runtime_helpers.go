package ai

import (
	"context"

	"github.com/rs/zerolog"
)

func (oc *AIClient) Log() *zerolog.Logger {
	if oc == nil {
		logger := zerolog.Nop()
		return &logger
	}
	return &oc.log
}

func (oc *AIClient) BackgroundContext(ctx context.Context) context.Context {
	if oc == nil {
		return ctx
	}
	return oc.backgroundContext(ctx)
}
