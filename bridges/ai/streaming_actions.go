package ai

import (
	"context"
	"strings"

	"github.com/openai/openai-go/v3/responses"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
)

type streamTurnActions struct {
	oc                         *AIClient
	ctx                        context.Context
	log                        zerolog.Logger
	portal                     *bridgev2.Portal
	state                      *streamingState
	meta                       *PortalMetadata
	activeTools                *streamToolRegistry
	typingSignals              *TypingSignaler
	touchTyping                func()
	continuationSuffix         string
	checkApprovalWithoutObject bool
}

func newStreamTurnActions(
	ctx context.Context,
	oc *AIClient,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	activeTools *streamToolRegistry,
	typingSignals *TypingSignaler,
	touchTyping func(),
	isContinuation bool,
	checkApprovalWithoutObject bool,
) streamTurnActions {
	suffix := ""
	if isContinuation {
		suffix = " (continuation)"
	}
	return streamTurnActions{
		oc:                         oc,
		ctx:                        ctx,
		log:                        log,
		portal:                     portal,
		state:                      state,
		meta:                       meta,
		activeTools:                activeTools,
		typingSignals:              typingSignals,
		touchTyping:                touchTyping,
		continuationSuffix:         suffix,
		checkApprovalWithoutObject: checkApprovalWithoutObject,
	}
}

func (a streamTurnActions) touch() {
	if a.touchTyping != nil {
		a.touchTyping()
	}
}

func (a streamTurnActions) touchTool() {
	a.touch()
	if a.typingSignals != nil {
		a.typingSignals.SignalToolStart()
	}
}

func (a streamTurnActions) textErrorText() string {
	return "failed to send initial streaming message" + a.continuationSuffix
}

func (a streamTurnActions) textLogMessage() string {
	return "Failed to send initial streaming message" + a.continuationSuffix
}

func (a streamTurnActions) updateUsage(promptTokens, completionTokens, reasoningTokens, totalTokens int64) {
	if a.state == nil {
		return
	}
	a.state.promptTokens = promptTokens
	a.state.completionTokens = completionTokens
	a.state.reasoningTokens = reasoningTokens
	a.state.totalTokens = totalTokens
	a.state.writer().MessageMetadata(a.ctx, a.oc.buildUIMessageMetadata(a.state, a.meta, true))
}

func (a streamTurnActions) textDelta(delta string) (string, error) {
	a.touch()
	return a.oc.processStreamingTextDelta(
		a.ctx,
		a.log,
		a.portal,
		a.state,
		a.meta,
		a.typingSignals,
		delta,
		a.textErrorText(),
		a.textLogMessage(),
	)
}

func (a streamTurnActions) reasoningDelta(delta string) error {
	a.touch()
	if a.typingSignals != nil {
		a.typingSignals.SignalReasoningDelta()
	}
	return a.oc.handleResponseReasoningTextDelta(
		a.ctx,
		a.log,
		a.portal,
		a.state,
		a.meta,
		delta,
		a.textErrorText(),
		a.textLogMessage(),
	)
}

func (a streamTurnActions) reasoningText(text string) {
	a.oc.appendReasoningText(a.ctx, a.portal, a.state, strings.TrimSpace(text))
}

func (a streamTurnActions) refusalDelta(delta string) {
	a.touch()
	a.oc.handleResponseRefusalDelta(a.ctx, a.portal, a.state, a.typingSignals, delta)
}

func (a streamTurnActions) refusalDone(refusal string) {
	a.oc.handleResponseRefusalDone(a.ctx, a.portal, a.state, strings.TrimSpace(refusal))
}

func (a streamTurnActions) functionToolInputDelta(itemID, name, delta string) {
	a.touchTool()
	a.oc.handleFunctionCallArgumentsDelta(a.ctx, a.portal, a.state, a.meta, a.activeTools, itemID, name, delta)
}

func (a streamTurnActions) functionToolInputDone(itemID, name, arguments string) {
	a.touchTool()
	a.oc.handleFunctionCallArgumentsDone(
		a.ctx,
		a.log,
		a.portal,
		a.state,
		a.meta,
		a.activeTools,
		itemID,
		name,
		arguments,
		a.checkApprovalWithoutObject,
		a.continuationSuffix,
	)
}

func (a streamTurnActions) outputItemAdded(item responses.ResponseOutputItemUnion) {
	a.oc.handleResponseOutputItemAdded(a.ctx, a.portal, a.state, a.activeTools, item)
}

func (a streamTurnActions) outputItemDone(item responses.ResponseOutputItemUnion) {
	a.oc.handleResponseOutputItemDone(a.ctx, a.portal, a.state, a.activeTools, item)
}

func (a streamTurnActions) annotationAdded(annotation any, annotationIndex any) {
	a.oc.handleResponseOutputAnnotationAdded(a.ctx, a.portal, a.state, annotation, annotationIndex)
}

// toolResultCompleted finalises a tool call from a Responses API output item
// through the actions layer, consolidating status-to-result mapping.
func (a streamTurnActions) toolResultCompleted(tool *activeToolCall, item responses.ResponseOutputItemUnion) {
	a.touch()
	newToolLifecycle(a.state).completeFromResponseItem(a.ctx, tool, item)
}

// finalizeMetadata emits a consolidated metadata update on the writer.
func (a streamTurnActions) finalizeMetadata() {
	if a.state == nil {
		return
	}
	a.state.writer().MessageMetadata(a.ctx, a.oc.buildUIMessageMetadata(a.state, a.meta, true))
}
