package ai

import (
	"context"
	"strings"
	"time"

	"github.com/openai/openai-go/v3/responses"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/matrixevents"
	"github.com/beeper/agentremote/pkg/shared/streamui"
)

func (oc *AIClient) upsertActiveToolFromDescriptor(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools *streamToolRegistry,
	desc responseToolDescriptor,
) (*activeToolCall, bool) {
	if activeTools == nil || strings.TrimSpace(desc.callID) == "" {
		return nil, false
	}
	lifecycle := newToolLifecycle(state)
	tool, created := activeTools.Upsert(desc.registryKey, func(canonicalKey string) *activeToolCall {
		return &activeToolCall{
			callID:      SanitizeToolCallID(desc.callID, "strict"),
			toolName:    desc.toolName,
			toolType:    desc.toolType,
			startedAtMs: time.Now().UnixMilli(),
			itemID:      desc.itemID,
		}
	})
	if created && strings.TrimSpace(desc.itemID) == "" {
		zerolog.Ctx(ctx).Warn().Str("registry_key", desc.registryKey).Msg("active tool created without item id")
	}
	if tool == nil {
		return nil, false
	}
	if strings.TrimSpace(desc.callID) != "" {
		tool.callID = SanitizeToolCallID(desc.callID, "strict")
	}
	if strings.TrimSpace(desc.itemID) != "" {
		tool.itemID = desc.itemID
		activeTools.BindAlias(streamToolItemKey(desc.itemID), tool)
	}
	activeTools.BindAlias(streamToolCallKey(tool.callID), tool)
	if strings.TrimSpace(desc.toolName) != "" {
		tool.toolName = desc.toolName
	}
	if desc.toolType != "" {
		tool.toolType = desc.toolType
	}
	if state != nil && state.turn != nil {
		streamui.TrackTool(state.turn.UIState(), tool.callID, tool.toolName, tool.toolType)
	}

	if created {
		lifecycle.ensureInputStart(ctx, tool, desc.providerExecuted, nil)
	}
	return tool, created
}

func (oc *AIClient) ensureActiveToolForStreamItem(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools *streamToolRegistry,
	itemID string,
	item responses.ResponseOutputItemUnion,
) *activeToolCall {
	if activeTools == nil || state == nil {
		return nil
	}
	if tool := activeTools.Lookup(streamToolItemKey(itemID)); tool != nil {
		return tool
	}
	itemDesc := deriveToolDescriptorForOutputItem(item, state)
	if !itemDesc.ok {
		return nil
	}
	tool, _ := oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
	return tool
}

func (oc *AIClient) handleCustomToolInputDeltaFromOutputItem(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools *streamToolRegistry,
	itemID string,
	item responses.ResponseOutputItemUnion,
	delta string,
) {
	lifecycle := newToolLifecycle(state)
	tool := oc.ensureActiveToolForStreamItem(ctx, portal, state, activeTools, itemID, item)
	if tool == nil {
		return
	}
	lifecycle.appendInputDelta(ctx, tool, tool.toolName, delta, tool.toolType == matrixevents.ToolTypeProvider)
}

func (oc *AIClient) handleCustomToolInputDoneFromOutputItem(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools *streamToolRegistry,
	itemID string,
	item responses.ResponseOutputItemUnion,
	inputText string,
) {
	lifecycle := newToolLifecycle(state)
	tool := oc.ensureActiveToolForStreamItem(ctx, portal, state, activeTools, itemID, item)
	if tool == nil {
		return
	}
	if tool.input.Len() == 0 && strings.TrimSpace(inputText) != "" {
		tool.input.WriteString(inputText)
	}
	lifecycle.emitInput(ctx, tool, tool.toolName, parseJSONOrRaw(tool.input.String()), tool.toolType == matrixevents.ToolTypeProvider)
}

// resolveOutputItemTool performs the common setup shared by handleResponseOutputItemAdded
// and handleResponseOutputItemDone: derives the tool descriptor, upserts the active tool,
// and checks finalization.
// Returns (tool, desc, ok). When ok is false the caller should return early.
func (oc *AIClient) resolveOutputItemTool(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools *streamToolRegistry,
	item responses.ResponseOutputItemUnion,
) (*activeToolCall, responseToolDescriptor, bool, bool) {
	desc := deriveToolDescriptorForOutputItem(item, state)
	if !desc.ok || state == nil {
		return nil, desc, false, false
	}
	tool, created := oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, desc)
	if tool == nil {
		return nil, desc, false, false
	}
	if state != nil && state.turn != nil {
		if state.turn.UIState().UIToolOutputFinalized[tool.callID] {
			return nil, desc, false, false
		}
	}
	return tool, desc, created, true
}

// emitToolInputIfAvailable records the tool's input text and emits a UI input-available
// event when the descriptor carries a non-nil input payload.
func (oc *AIClient) emitToolInputIfAvailable(ctx context.Context, portal *bridgev2.Portal, state *streamingState, tool *activeToolCall, desc responseToolDescriptor) {
	if desc.input == nil {
		return
	}
	if tool.input.Len() == 0 {
		tool.input.WriteString(stringifyJSONValue(desc.input))
	}
	newToolLifecycle(state).emitInput(ctx, tool, tool.toolName, desc.input, desc.providerExecuted)
}

func (oc *AIClient) handleResponseOutputItemAdded(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools *streamToolRegistry,
	item responses.ResponseOutputItemUnion,
) {
	tool, desc, created, ok := oc.resolveOutputItemTool(ctx, portal, state, activeTools, item)
	if !ok {
		return
	}
	if created || desc.input != nil {
		oc.emitToolInputIfAvailable(ctx, portal, state, tool, desc)
	}
}

func (oc *AIClient) handleResponseOutputItemDone(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools *streamToolRegistry,
	item responses.ResponseOutputItemUnion,
) {
	tool, desc, created, ok := oc.resolveOutputItemTool(ctx, portal, state, activeTools, item)
	if !ok {
		return
	}
	if created || desc.input != nil {
		oc.emitToolInputIfAvailable(ctx, portal, state, tool, desc)
	}

	actions := streamTurnActions{oc: oc, ctx: ctx, portal: portal, state: state}
	actions.toolResultCompleted(tool, item)
}

// Response stream output helpers.
