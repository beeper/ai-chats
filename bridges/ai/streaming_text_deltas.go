package ai

import (
	"context"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"

	runtimeparse "github.com/beeper/agentremote/pkg/runtime"

	"github.com/beeper/agentremote/pkg/shared/citations"
)

func (oc *AIClient) emitVisibleTextDelta(
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
	if typingSignals != nil {
		typingSignals.SignalTextDelta(delta)
	}
	if delta == "" {
		return nil
	}
	state.trackFirstToken()
	// Writer.TextDelta triggers Turn.ensureStarted on first call,
	// which sends the placeholder message via the configured SendFunc.
	state.writer().TextDelta(ctx, delta)
	if err := state.turn.Err(); err != nil {
		log.Error().Err(err).Msg(logMessage)
		state.finishReason = "error"
		state.writer().Error(ctx, errText)
		return err
	}
	// Sync IDs from Turn after initial message is sent.
	return nil
}

func (oc *AIClient) processStreamingTextDelta(
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
) (string, error) {
	delta = maybePrependTextSeparator(state, delta)
	state.accumulated.WriteString(delta)

	roundDelta := delta
	var parsed *runtimeparse.StreamingDirectiveResult
	if state.replyAccumulator != nil {
		parsed = state.replyAccumulator.Consume(delta, false)
	}
	if parsed == nil {
		if err := oc.emitVisibleTextDelta(
			ctx,
			log,
			portal,
			state,
			meta,
			typingSignals,
			isHeartbeat,
			roundDelta,
			errText,
			logMessage,
		); err != nil {
			return "", err
		}
		return roundDelta, nil
	}

	oc.applyStreamingReplyTarget(state, parsed)
	roundDelta = parsed.Text
	if roundDelta == "" {
		return roundDelta, nil
	}

	if err := oc.emitVisibleTextDelta(
		ctx,
		log,
		portal,
		state,
		meta,
		typingSignals,
		isHeartbeat,
		roundDelta,
		errText,
		logMessage,
	); err != nil {
		return "", err
	}
	return roundDelta, nil
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
	state.trackFirstToken()
	state.writer().ReasoningDelta(ctx, delta)
	if err := state.turn.Err(); err != nil {
		log.Error().Err(err).Msg(logMessage)
		state.finishReason = "error"
		state.writer().Error(ctx, errText)
		return err
	}
	return nil
}

// appendReasoningText appends non-empty reasoning/summary text to state and emits a UI delta.
// Used by both reasoning_summary_text.delta and reasoning_text.done / reasoning_summary_text.done.
func (oc *AIClient) appendReasoningText(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	text string,
) {
	if text == "" {
		return
	}
	state.reasoning.WriteString(text)
	state.writer().ReasoningDelta(ctx, text)
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
	state.writer().TextDelta(ctx, delta)
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
	state.writer().TextDelta(ctx, refusal)
}

func (oc *AIClient) handleResponseOutputAnnotationAdded(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	annotation any,
	annotationIndex any,
) {
	stream := state.writer()
	if citation, ok := extractURLCitation(annotation); ok {
		state.sourceCitations = citations.AppendUniqueCitation(state.sourceCitations, citation)
		stream.SourceURL(ctx, citation)
	}
	if document, ok := extractDocumentCitation(annotation); ok {
		state.sourceDocuments = append(state.sourceDocuments, document)
		stream.SourceDocument(ctx, document)
	}
	stream.Data(ctx, "annotation", map[string]any{"annotation": annotation, "index": annotationIndex}, true)
}
