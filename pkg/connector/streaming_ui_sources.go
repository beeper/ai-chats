package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/shared/citations"
)

func (oc *AIClient) emitUISourceURL(ctx context.Context, portal *bridgev2.Portal, state *streamingState, citation citations.SourceCitation) {
	oc.uiEmitter(state).EmitUISourceURL(ctx, portal, citation)
}

func (oc *AIClient) emitUISourceDocument(ctx context.Context, portal *bridgev2.Portal, state *streamingState, doc citations.SourceDocument) {
	oc.uiEmitter(state).EmitUISourceDocument(ctx, portal, doc)
}

func (oc *AIClient) emitUIFile(ctx context.Context, portal *bridgev2.Portal, state *streamingState, fileURL, mediaType string) {
	oc.uiEmitter(state).EmitUIFile(ctx, portal, fileURL, mediaType)
}

func collectToolOutputCitations(state *streamingState, toolName, output string) {
	if state == nil {
		return
	}
	extracted := extractWebSearchCitationsFromToolOutput(toolName, output)
	if len(extracted) == 0 {
		return
	}
	state.sourceCitations = citations.MergeSourceCitations(state.sourceCitations, extracted)
}
