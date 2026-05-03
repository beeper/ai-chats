package ai

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	"github.com/openai/openai-go/v3/responses"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-chats/pkg/shared/streamui"
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

	if item.Type == "image_generation_call" {
		oc.completeImageGenerationTool(ctx, portal, state, tool, item)
		return
	}

	actions := streamTurnActions{oc: oc, ctx: ctx, portal: portal, state: state}
	actions.toolResultCompleted(tool, item)
}

func (oc *AIClient) completeImageGenerationTool(ctx context.Context, portal *bridgev2.Portal, state *streamingState, tool *activeToolCall, item responses.ResponseOutputItemUnion) {
	lifecycle := newToolLifecycle(state)
	if strings.TrimSpace(item.Result) == "" {
		lifecycle.fail(ctx, tool, true, ResultStatusError, "image generation returned no image", nil)
		return
	}
	data, err := base64.StdEncoding.DecodeString(item.Result)
	if err != nil {
		lifecycle.fail(ctx, tool, true, ResultStatusError, "image generation returned invalid image data", nil)
		return
	}
	eventID, uri, err := oc.sendGeneratedMedia(ctx, portal, data, "image/png", currentStreamingTurnID(state), event.MsgImage, "generated.png", BeeperAIKey, false, "")
	if err != nil {
		lifecycle.fail(ctx, tool, true, ResultStatusError, err.Error(), nil)
		return
	}
	recordGeneratedFile(state, uri, "image/png")
	output := map[string]any{
		"status":   item.Status,
		"event_id": eventID.String(),
		"url":      uri,
	}
	lifecycle.succeed(ctx, tool, true, output, output, nil)
}

// Response stream output helpers.
