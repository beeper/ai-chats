package connector

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/shared/citations"
)

func (oc *AIClient) ensureInitialStreamMessage(
	ctx context.Context,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	isHeartbeat bool,
	initialText string,
	errText string,
	logMessage string,
) error {
	if !state.firstToken {
		return nil
	}
	state.firstToken = false
	state.firstTokenAtMs = time.Now().UnixMilli()

	if !state.suppressSend && !isHeartbeat {
		oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
		state.initialEventID = oc.sendInitialStreamMessage(ctx, portal, initialText, state.turnID, state.replyTarget)
		if state.initialEventID == "" {
			log.Error().Msg(logMessage)
			state.finishReason = "error"
			oc.uiEmitter(state).EmitUIError(ctx, portal, errText)
			oc.emitUIFinish(ctx, portal, state, meta)
			return errors.New(errText)
		}
	}
	return nil
}

func (oc *AIClient) handleResponseOutputTextDelta(
	ctx context.Context,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	typingSignals *TypingSignaler,
	isHeartbeat bool,
	delta string,
	errText string,
	logMessage string,
) error {
	delta = maybePrependTextSeparator(state, delta)
	state.accumulated.WriteString(delta)

	var parsed *streamingDirectiveResult
	if state.replyAccumulator != nil {
		parsed = state.replyAccumulator.Consume(delta, false)
	}
	if parsed == nil {
		return nil
	}

	oc.applyStreamingReplyTarget(state, parsed)
	cleaned := parsed.Text
	if typingSignals != nil {
		typingSignals.SignalTextDelta(cleaned)
	}
	if cleaned == "" {
		return nil
	}

	state.visibleAccumulated.WriteString(cleaned)
	if state.firstToken && state.visibleAccumulated.Len() > 0 {
		if err := oc.ensureInitialStreamMessage(
			ctx,
			log,
			portal,
			state,
			meta,
			isHeartbeat,
			state.visibleAccumulated.String(),
			errText,
			logMessage,
		); err != nil {
			return err
		}
	}
	oc.uiEmitter(state).EmitUITextDelta(ctx, portal, cleaned)
	return nil
}

func (oc *AIClient) handleResponseReasoningTextDelta(
	ctx context.Context,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	isHeartbeat bool,
	delta string,
	errText string,
	logMessage string,
) error {
	state.reasoning.WriteString(delta)
	if state.firstToken && state.reasoning.Len() > 0 {
		if err := oc.ensureInitialStreamMessage(
			ctx,
			log,
			portal,
			state,
			meta,
			isHeartbeat,
			"...",
			errText,
			logMessage,
		); err != nil {
			return err
		}
	}
	oc.uiEmitter(state).EmitUIReasoningDelta(ctx, portal, delta)
	return nil
}

func (oc *AIClient) handleResponseReasoningSummaryDelta(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	delta string,
) {
	if delta == "" {
		return
	}
	state.reasoning.WriteString(delta)
	oc.uiEmitter(state).EmitUIReasoningDelta(ctx, portal, delta)
}

func (oc *AIClient) handleResponseReasoningDone(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	text string,
) {
	if text == "" {
		return
	}
	state.reasoning.WriteString(text)
	oc.uiEmitter(state).EmitUIReasoningDelta(ctx, portal, text)
}

func (oc *AIClient) handleResponseRefusalDelta(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	typingSignals *TypingSignaler,
	delta string,
) {
	if typingSignals != nil {
		typingSignals.SignalTextDelta(delta)
	}
	oc.uiEmitter(state).EmitUITextDelta(ctx, portal, delta)
}

func (oc *AIClient) handleResponseRefusalDone(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	refusal string,
) {
	if refusal == "" {
		return
	}
	oc.uiEmitter(state).EmitUITextDelta(ctx, portal, refusal)
}

func (oc *AIClient) handleResponseOutputAnnotationAdded(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	annotation any,
	annotationIndex any,
) {
	if citation, ok := extractURLCitation(annotation); ok {
		state.sourceCitations = citations.AppendUniqueCitation(state.sourceCitations, citation)
		oc.uiEmitter(state).EmitUISourceURL(ctx, portal, citation)
	}
	if document, ok := extractDocumentCitation(annotation); ok {
		state.sourceDocuments = append(state.sourceDocuments, document)
		oc.uiEmitter(state).EmitUISourceDocument(ctx, portal, document)
	}
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":      "data-annotation",
		"data":      map[string]any{"annotation": annotation, "index": annotationIndex},
		"transient": true,
	})
}
