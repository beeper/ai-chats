package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

type responseFuncCanonical func(ctx context.Context, evt *event.Event, portal *bridgev2.Portal, meta *PortalMetadata, prompt PromptContext) (bool, *ContextLengthError, error)

func (oc *AIClient) selectStreamingRunFunc(meta *PortalMetadata, promptContext PromptContext) (responseFuncCanonical, string) {
	return oc.runResponsesStreamingPrompt, "responses"
}
