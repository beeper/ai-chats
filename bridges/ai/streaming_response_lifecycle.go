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
	if state == nil {
		return
	}
	if strings.TrimSpace(response.ID) != "" {
		state.responseID = response.ID
	}
	if status := strings.TrimSpace(string(response.Status)); status != "" {
		state.responseStatus = status
	}

	switch eventType {
	case "response.created", "response.queued", "response.in_progress":
		// No additional terminal state changes needed.
	case "response.completed":
		if state.responseStatus == "completed" {
			state.finishReason = "stop"
		} else {
			state.finishReason = state.responseStatus
		}
	case "response.failed":
		state.finishReason = "error"
	case "response.incomplete":
		state.finishReason = strings.TrimSpace(string(response.IncompleteDetails.Reason))
		if state.finishReason == "" {
			state.finishReason = "other"
		}
	default:
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
