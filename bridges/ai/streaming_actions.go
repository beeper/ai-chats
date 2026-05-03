package ai

import (
	"context"
	"strconv"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/matrixevents"
)

type streamTurnActions struct {
	oc                           *AIClient
	ctx                          context.Context
	log                          zerolog.Logger
	portal                       *bridgev2.Portal
	state                        *streamingState
	meta                         *PortalMetadata
	activeTools                  *streamToolRegistry
	typingSignals                *TypingSignaler
	touchTyping                  func()
	isHeartbeat                  bool
	continuationSuffix           string
	approvalFallbackForNonObject bool
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
	isHeartbeat bool,
	isContinuation bool,
	approvalFallbackForNonObject bool,
) streamTurnActions {
	suffix := ""
	if isContinuation {
		suffix = " (continuation)"
	}
	return streamTurnActions{
		oc:                           oc,
		ctx:                          ctx,
		log:                          log,
		portal:                       portal,
		state:                        state,
		meta:                         meta,
		activeTools:                  activeTools,
		typingSignals:                typingSignals,
		touchTyping:                  touchTyping,
		isHeartbeat:                  isHeartbeat,
		continuationSuffix:           suffix,
		approvalFallbackForNonObject: approvalFallbackForNonObject,
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
		a.isHeartbeat,
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
		a.isHeartbeat,
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
		a.approvalFallbackForNonObject,
		a.continuationSuffix,
	)
}

func (a streamTurnActions) outputItemAdded(item responses.ResponseOutputItemUnion) {
	a.oc.handleResponseOutputItemAdded(a.ctx, a.portal, a.state, a.activeTools, item)
}

func (a streamTurnActions) outputItemDone(item responses.ResponseOutputItemUnion) {
	a.oc.handleResponseOutputItemDone(a.ctx, a.portal, a.state, a.activeTools, item)
}

func (a streamTurnActions) customToolInputDelta(itemID string, item responses.ResponseOutputItemUnion, delta string) {
	a.oc.handleCustomToolInputDeltaFromOutputItem(a.ctx, a.portal, a.state, a.activeTools, itemID, item, delta)
}

func (a streamTurnActions) customToolInputDone(itemID string, item responses.ResponseOutputItemUnion, inputText string) {
	a.oc.handleCustomToolInputDoneFromOutputItem(a.ctx, a.portal, a.state, a.activeTools, itemID, item, inputText)
}

func (a streamTurnActions) annotationAdded(annotation any, annotationIndex any) {
	a.oc.handleResponseOutputAnnotationAdded(a.ctx, a.portal, a.state, annotation, annotationIndex)
}

// toolResultCompleted finalises a tool call from a Responses API output item
// through the actions layer, consolidating status-to-result mapping.
func (a streamTurnActions) toolResultCompleted(tool *activeToolCall, item responses.ResponseOutputItemUnion) {
	a.touch()
	a.oc.toolLifecycle(a.portal, a.state).completeFromResponseItem(a.ctx, tool, item)
}

// emitCustomToolInput handles the common delta/done pattern for custom tool and
// code-interpreter argument events.
func (a streamTurnActions) emitCustomToolInput(itemID string, item responses.ResponseOutputItemUnion, isDelta bool, content string) {
	if isDelta {
		a.customToolInputDelta(itemID, item, content)
	} else {
		a.customToolInputDone(itemID, item, content)
	}
}

// finalizeMetadata emits a consolidated metadata update on the writer.
func (a streamTurnActions) finalizeMetadata() {
	if a.state == nil {
		return
	}
	a.state.writer().MessageMetadata(a.ctx, a.oc.buildUIMessageMetadata(a.state, a.meta, true))
}

func chatToolRegistryKey(index int64) string {
	return "chat-index:" + strconv.FormatInt(index, 10)
}

func chatToolDescriptor(toolDelta openai.ChatCompletionChunkChoiceDeltaToolCall) responseToolDescriptor {
	desc := responseToolDescriptor{
		registryKey: streamToolItemKey(chatToolRegistryKey(toolDelta.Index)),
		itemID:      chatToolRegistryKey(toolDelta.Index),
		callID:      strings.TrimSpace(toolDelta.ID),
		toolName:    strings.TrimSpace(toolDelta.Function.Name),
		toolType:    matrixevents.ToolTypeFunction,
		ok:          true,
	}
	if desc.callID == "" {
		desc.callID = desc.itemID
	}
	if desc.registryKey == "" {
		desc.registryKey = streamToolCallKey(desc.callID)
	}
	return desc
}

func (a streamTurnActions) chatToolInputDelta(toolDelta openai.ChatCompletionChunkChoiceDeltaToolCall) *activeToolCall {
	a.touchTool()
	desc := chatToolDescriptor(toolDelta)
	tool, _ := a.oc.upsertActiveToolFromDescriptor(a.ctx, a.portal, a.state, a.activeTools, desc)
	if tool == nil {
		return nil
	}
	if tool.input.Len() == 0 {
		a.oc.toolLifecycle(a.portal, a.state).ensureInputStart(a.ctx, tool, false, nil)
	}
	if desc.toolName != "" {
		tool.toolName = desc.toolName
	}
	if toolDelta.Function.Arguments != "" {
		a.oc.toolLifecycle(a.portal, a.state).appendInputDelta(a.ctx, tool, tool.toolName, toolDelta.Function.Arguments, false)
	}
	return tool
}
