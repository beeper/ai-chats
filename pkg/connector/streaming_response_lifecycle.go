package connector

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
	switch eventType {
	case "response.created", "response.queued", "response.in_progress":
		if strings.TrimSpace(response.ID) != "" {
			state.responseID = response.ID
		}
		oc.emitUIRuntimeMetadata(ctx, portal, state, meta, responseMetadataDeltaFromResponse(response))
	case "response.failed":
		state.finishReason = "error"
		if strings.TrimSpace(response.ID) != "" {
			state.responseID = response.ID
		}
		oc.emitUIRuntimeMetadata(ctx, portal, state, meta, responseMetadataDeltaFromResponse(response))
		if msg := strings.TrimSpace(response.Error.Message); msg != "" {
			oc.emitUIError(ctx, portal, state, msg)
		}
	case "response.incomplete":
		state.finishReason = strings.TrimSpace(string(response.IncompleteDetails.Reason))
		if strings.TrimSpace(state.finishReason) == "" {
			state.finishReason = "other"
		}
		if strings.TrimSpace(response.ID) != "" {
			state.responseID = response.ID
		}
		oc.emitUIRuntimeMetadata(ctx, portal, state, meta, responseMetadataDeltaFromResponse(response))
	}
}
