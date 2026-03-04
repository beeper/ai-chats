package providers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func isContextAborted(runCtx context.Context, err error) bool {
	if runCtx != nil {
		ctxErr := runCtx.Err()
		if errors.Is(ctxErr, context.Canceled) || errors.Is(ctxErr, context.DeadlineExceeded) {
			return true
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if err == nil {
		return false
	}
	lowerErr := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(lowerErr, "context canceled") ||
		strings.Contains(lowerErr, "context cancelled") ||
		strings.Contains(lowerErr, "deadline exceeded")
}

func pushProviderAborted(stream *ai.AssistantMessageEventStream, model ai.Model) {
	stream.Push(ai.AssistantMessageEvent{
		Type: ai.EventDone,
		Message: ai.Message{
			Role:       ai.RoleAssistant,
			API:        model.API,
			Provider:   model.Provider,
			Model:      model.ID,
			StopReason: ai.StopReasonAborted,
			Timestamp:  time.Now().UnixMilli(),
		},
		Reason: ai.StopReasonAborted,
	})
}
