package ai

import (
	"context"
	"strings"

	"github.com/openai/openai-go/v3/responses"
	"maunium.net/go/mautrix/bridgev2"
)

func (oc *AIClient) handleResponseLifecycleEvent(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	eventType string,
	response responses.Response,
) {
	if !applyResponseLifecycleState(state, eventType, response) {
		return
	}

	base := oc.buildUIMessageMetadata(state, meta, false)
	extra := responseMetadataDeltaFromResponse(response)
	if len(extra) > 0 {
		base = mergeMaps(base, extra)
	}
	state.writer().MessageMetadata(ctx, base)

	if eventType == "response.failed" {
		if msg := strings.TrimSpace(response.Error.Message); msg != "" {
			state.writer().Error(ctx, msg)
		}
	}
}

func applyResponseLifecycleState(
	state *streamingState,
	eventType string,
	response responses.Response,
) bool {
	if state == nil {
		return false
	}
	if strings.TrimSpace(response.ID) != "" {
		state.responseID = response.ID
	}
	if status := strings.TrimSpace(string(response.Status)); status != "" {
		state.responseStatus = status
	}
	switch eventType {
	case "response.created", "response.queued", "response.in_progress", "response.completed":
		// No additional state changes needed.
	case "response.failed":
		state.finishReason = "error"
	case "response.incomplete":
		state.finishReason = strings.TrimSpace(string(response.IncompleteDetails.Reason))
		if state.finishReason == "" {
			state.finishReason = "other"
		}
	default:
		return false
	}
	return true
}
