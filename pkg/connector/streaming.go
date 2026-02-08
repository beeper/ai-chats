package connector

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/openai/openai-go/v3/shared/constant"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/agents/tools"
)

// streamingState tracks the state of a streaming response
type streamingState struct {
	turnID         string
	agentID        string
	startedAtMs    int64
	firstTokenAtMs int64
	completedAtMs  int64
	roomID         id.RoomID

	promptTokens     int64
	completionTokens int64
	reasoningTokens  int64
	totalTokens      int64

	baseInput              responses.ResponseInputParam
	accumulated            strings.Builder
	visibleAccumulated     strings.Builder
	reasoning              strings.Builder
	toolCalls              []ToolCallMetadata
	pendingImages          []generatedImage
	pendingFunctionOutputs []functionCallOutput // Function outputs to send back to API for continuation
	sourceCitations        []sourceCitation
	sourceDocuments        []sourceDocument
	generatedFiles         []generatedFilePart
	initialEventID         id.EventID
	finishReason           string
	responseID             string
	sequenceNum            int
	firstToken             bool
	statusSent             bool
	statusSentIDs          map[id.EventID]bool

	// Directive processing
	sourceEventID    id.EventID // The triggering user message event ID (for [[reply_to_current]])
	senderID         string     // The triggering sender ID (for owner-only tool gating)
	replyTarget      ReplyTarget
	replyAccumulator *streamingDirectiveAccumulator

	// Heartbeat handling
	heartbeat         *HeartbeatRunConfig
	heartbeatResultCh chan HeartbeatRunOutcome
	suppressSave      bool
	suppressSend      bool

	// AI SDK UIMessage stream tracking
	uiStarted               bool
	uiFinished              bool
	uiTextID                string
	uiReasoningID           string
	uiStepOpen              bool
	uiStepCount             int
	uiToolStarted           map[string]bool
	uiSourceURLSeen         map[string]bool
	uiSourceDocumentSeen    map[string]bool
	uiFileSeen              map[string]bool
	uiToolCallIDByApproval  map[string]string
	uiToolApprovalRequested map[string]bool
	uiToolNameByToolCallID  map[string]string
	uiToolTypeByToolCallID  map[string]ToolType
	uiToolOutputFinalized   map[string]bool

	// Pending MCP approvals to resolve before the turn can continue.
	pendingMcpApprovals     []mcpApprovalRequest
	pendingMcpApprovalsSeen map[string]bool

	// Avoid logging repeated missing-ephemeral warnings.
	streamEphemeralUnsupported bool

	// Debounced ephemeral logging: true once the "Streaming started" summary has been logged.
	loggedStreamStart bool

	// Codex (app-server) specific streaming helpers.
	// Only used by CodexClient; kept here to reuse the existing AI SDK chunk plumbing.
	codexToolOutputBuffers    map[string]*strings.Builder // toolCallId -> accumulated output
	codexLatestDiff           string                      // last turn/diff/updated diff snapshot
	codexReasoningSummarySeen bool                        // whether summary reasoning deltas have been seen
	// Used by CodexClient to avoid spamming timeline notices for repeated signals.
	codexTimelineNotices map[string]bool
}

type mcpApprovalRequest struct {
	approvalID  string
	toolCallID  string
	toolName    string
	serverLabel string
}

// newStreamingState creates a new streaming state with initialized fields
func newStreamingState(ctx context.Context, meta *PortalMetadata, sourceEventID id.EventID, senderID string, roomID id.RoomID) *streamingState {
	agentID := ""
	if meta != nil {
		agentID = resolveAgentID(meta)
	}
	state := &streamingState{
		turnID:                  NewTurnID(),
		agentID:                 agentID,
		startedAtMs:             time.Now().UnixMilli(),
		firstToken:              true,
		sourceEventID:           sourceEventID,
		senderID:                senderID,
		roomID:                  roomID,
		statusSentIDs:           make(map[id.EventID]bool),
		replyAccumulator:        newStreamingDirectiveAccumulator(),
		uiToolStarted:           make(map[string]bool),
		uiSourceURLSeen:         make(map[string]bool),
		uiSourceDocumentSeen:    make(map[string]bool),
		uiFileSeen:              make(map[string]bool),
		uiToolCallIDByApproval:  make(map[string]string),
		uiToolApprovalRequested: make(map[string]bool),
		uiToolNameByToolCallID:  make(map[string]string),
		uiToolTypeByToolCallID:  make(map[string]ToolType),
		uiToolOutputFinalized:   make(map[string]bool),
		pendingMcpApprovalsSeen: make(map[string]bool),
		codexTimelineNotices:    make(map[string]bool),
	}
	if meta != nil && normalizeSendPolicyMode(meta.SendPolicy) == "deny" {
		state.suppressSend = true
	}
	if hb := heartbeatRunFromContext(ctx); hb != nil {
		state.heartbeat = hb.Config
		state.heartbeatResultCh = hb.ResultCh
		if hb.Config != nil && hb.Config.SuppressSave {
			state.suppressSave = true
		}
		if hb.Config != nil && hb.Config.SuppressSend {
			state.suppressSend = true
		}
	}
	return state
}

func (oc *AIClient) applyStreamingReplyTarget(state *streamingState, parsed *streamingDirectiveResult) {
	if oc == nil || state == nil || parsed == nil || !parsed.HasReplyTag {
		return
	}
	if oc.resolveMatrixReplyToMode() == "off" {
		return
	}
	if parsed.ReplyToExplicitID != "" {
		state.replyTarget.ReplyTo = id.EventID(parsed.ReplyToExplicitID)
		return
	}
	if parsed.ReplyToCurrent && state.sourceEventID != "" {
		state.replyTarget.ReplyTo = state.sourceEventID
	}
}

func (oc *AIClient) markMessageSendSuccess(ctx context.Context, portal *bridgev2.Portal, evt *event.Event, state *streamingState) {
	if state == nil || state.suppressSend {
		return
	}
	if queueAcceptedStatusFromContext(ctx) {
		return
	}
	if state.statusSentIDs == nil {
		state.statusSentIDs = make(map[id.EventID]bool)
	}
	events := make([]*event.Event, 0, 4)
	if evt != nil {
		events = append(events, evt)
	}
	events = append(events, statusEventsFromContext(ctx)...)
	if portal != nil && portal.MXID != "" {
		events = append(events, oc.roomRunStatusEvents(portal.MXID)...)
	}
	for _, extra := range events {
		if extra == nil || extra.ID == "" {
			continue
		}
		if state.statusSentIDs[extra.ID] {
			continue
		}
		oc.sendSuccessStatus(ctx, portal, extra)
		state.statusSentIDs[extra.ID] = true
	}
	if len(state.statusSentIDs) > 0 {
		state.statusSent = true
	}
}

// generatedImage tracks a pending image from image generation
type generatedImage struct {
	itemID   string
	imageB64 string
	turnID   string
}

type generatedFilePart struct {
	url       string
	mediaType string
}

// functionCallOutput tracks a completed function call output for API continuation
type functionCallOutput struct {
	callID    string // The ItemID from the stream event (used as call_id in continuation)
	name      string // Tool name (for stateless continuations)
	arguments string // Raw arguments JSON (for stateless continuations)
	output    string // The result from executing the tool
}

func buildFunctionCallOutputItem(callID, output string, includeID bool) responses.ResponseInputItemUnionParam {
	item := responses.ResponseInputItemFunctionCallOutputParam{
		CallID: callID,
		Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
			OfString: param.NewOpt(output),
		},
	}
	if includeID {
		item.ID = param.NewOpt("fc_output_" + callID)
	}
	return responses.ResponseInputItemUnionParam{OfFunctionCallOutput: &item}
}

func recordGeneratedFile(state *streamingState, url, mediaType string) {
	if state == nil {
		return
	}
	url = strings.TrimSpace(url)
	if url == "" {
		return
	}
	for _, file := range state.generatedFiles {
		if file.url == url {
			return
		}
	}
	state.generatedFiles = append(state.generatedFiles, generatedFilePart{
		url:       url,
		mediaType: strings.TrimSpace(mediaType),
	})
}

func (oc *AIClient) buildUIMessageMetadata(state *streamingState, meta *PortalMetadata, includeUsage bool) map[string]any {
	metadata := map[string]any{
		"model":    oc.effectiveModel(meta),
		"turn_id":  state.turnID,
		"agent_id": state.agentID,
	}
	if includeUsage {
		metadata["usage"] = map[string]any{
			"prompt_tokens":     state.promptTokens,
			"completion_tokens": state.completionTokens,
			"reasoning_tokens":  state.reasoningTokens,
			"total_tokens":      state.totalTokens,
		}
		metadata["timing"] = map[string]any{
			"started_at":     state.startedAtMs,
			"first_token_at": state.firstTokenAtMs,
			"completed_at":   state.completedAtMs,
		}
		metadata["finish_reason"] = mapFinishReason(state.finishReason)
	}
	return metadata
}

func mapFinishReason(reason string) string {
	switch reason {
	case "stop":
		return "stop"
	case "end_turn", "end-turn":
		return "stop"
	case "length", "max_output_tokens":
		return "length"
	case "content_filter", "content-filter":
		return "content-filter"
	case "tool_calls", "tool-calls", "tool_use", "tool-use", "toolUse":
		return "tool-calls"
	case "error":
		return "error"
	default:
		return "other"
	}
}

func shouldContinueChatToolLoop(finishReason string, toolCallCount int) bool {
	if toolCallCount <= 0 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(finishReason)) {
	case "", "tool_calls", "tool-calls", "tool_use", "tool-use", "tooluse":
		return true
	default:
		return false
	}
}

func mergeMaps(base map[string]any, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func toJSONObject(value any) map[string]any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil
	}
	return decoded
}

func parseJSONOrRaw(input string) any {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return map[string]any{}
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return trimmed
	}
	return parsed
}

func stringifyJSONValue(value any) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(encoded))
}

func responseOutputItemToMap(item responses.ResponseOutputItemUnion) map[string]any {
	return toJSONObject(item)
}

type responseToolDescriptor struct {
	itemID           string
	callID           string
	toolName         string
	toolType         ToolType
	input            any
	providerExecuted bool
	dynamic          bool
	ok               bool
}

func deriveToolDescriptorForOutputItem(item responses.ResponseOutputItemUnion, state *streamingState) responseToolDescriptor {
	desc := responseToolDescriptor{
		itemID: item.ID,
		callID: item.ID,
	}
	switch item.Type {
	case "function_call":
		desc.callID = strings.TrimSpace(item.CallID)
		if desc.callID == "" {
			desc.callID = item.ID
		}
		desc.toolName = strings.TrimSpace(item.Name)
		desc.toolType = ToolTypeFunction
		desc.providerExecuted = false
		desc.dynamic = false
		desc.input = parseJSONOrRaw(item.Arguments)
		desc.ok = desc.toolName != ""
	case "web_search_call":
		desc.toolName = ToolNameWebSearch
		desc.toolType = ToolTypeProvider
		desc.providerExecuted = true
		desc.input = map[string]any{}
		desc.ok = true
	case "file_search_call":
		desc.toolName = "file_search"
		desc.toolType = ToolTypeProvider
		desc.providerExecuted = true
		desc.input = map[string]any{}
		desc.ok = true
	case "image_generation_call":
		desc.toolName = "image_generation"
		desc.toolType = ToolTypeProvider
		desc.providerExecuted = true
		desc.input = map[string]any{}
		desc.ok = true
	case "code_interpreter_call":
		desc.toolName = "code_interpreter"
		desc.toolType = ToolTypeProvider
		desc.providerExecuted = true
		desc.input = map[string]any{
			"containerId": item.ContainerID,
			"code":        item.Code,
		}
		desc.ok = true
	case "computer_call":
		desc.toolName = "computer_use"
		desc.toolType = ToolTypeProvider
		desc.providerExecuted = true
		desc.input = map[string]any{}
		desc.ok = true
	case "local_shell_call":
		desc.callID = strings.TrimSpace(item.CallID)
		if desc.callID == "" {
			desc.callID = item.ID
		}
		desc.toolName = "local_shell"
		desc.toolType = ToolTypeProvider
		desc.providerExecuted = true
		desc.dynamic = true
		desc.input = responseOutputItemToMap(item)
		desc.ok = true
	case "shell_call":
		desc.callID = strings.TrimSpace(item.CallID)
		if desc.callID == "" {
			desc.callID = item.ID
		}
		desc.toolName = "shell"
		desc.toolType = ToolTypeProvider
		desc.providerExecuted = true
		desc.dynamic = true
		desc.input = responseOutputItemToMap(item)
		desc.ok = true
	case "apply_patch_call":
		desc.callID = strings.TrimSpace(item.CallID)
		if desc.callID == "" {
			desc.callID = item.ID
		}
		desc.toolName = "apply_patch"
		desc.toolType = ToolTypeProvider
		desc.providerExecuted = true
		desc.dynamic = true
		desc.input = responseOutputItemToMap(item)
		desc.ok = true
	case "custom_tool_call":
		desc.callID = strings.TrimSpace(item.CallID)
		if desc.callID == "" {
			desc.callID = item.ID
		}
		desc.toolName = strings.TrimSpace(item.Name)
		desc.toolType = ToolTypeFunction
		desc.providerExecuted = false
		desc.dynamic = true
		desc.input = parseJSONOrRaw(item.Input)
		desc.ok = desc.toolName != ""
	case "mcp_call":
		desc.toolName = "mcp." + strings.TrimSpace(item.Name)
		desc.toolType = ToolTypeMCP
		desc.providerExecuted = true
		desc.dynamic = true
		if approvalID := strings.TrimSpace(item.ApprovalRequestID); approvalID != "" && state != nil {
			if mapped := strings.TrimSpace(state.uiToolCallIDByApproval[approvalID]); mapped != "" {
				desc.callID = mapped
			}
		}
		desc.input = parseJSONOrRaw(item.Arguments)
		desc.ok = strings.TrimSpace(item.Name) != ""
	case "mcp_list_tools":
		desc.toolName = "mcp.list_tools"
		desc.toolType = ToolTypeMCP
		desc.providerExecuted = true
		desc.dynamic = true
		desc.input = map[string]any{}
		desc.ok = true
	case "mcp_approval_request":
		desc.toolName = "mcp." + strings.TrimSpace(item.Name)
		desc.toolType = ToolTypeMCP
		desc.providerExecuted = true
		desc.dynamic = true
		desc.callID = NewCallID()
		desc.input = parseJSONOrRaw(item.Arguments)
		desc.ok = strings.TrimSpace(item.Name) != ""
	default:
		desc.ok = false
	}
	if strings.TrimSpace(desc.callID) == "" {
		desc.callID = NewCallID()
	}
	if desc.itemID == "" {
		desc.itemID = desc.callID
	}
	return desc
}

func outputItemLooksDenied(item responses.ResponseOutputItemUnion) bool {
	errorText := strings.ToLower(strings.TrimSpace(item.Error))
	if strings.Contains(errorText, "denied") || strings.Contains(errorText, "rejected") {
		return true
	}
	status := strings.ToLower(strings.TrimSpace(item.Status))
	return status == "denied" || status == "rejected"
}

func responseOutputItemResultPayload(item responses.ResponseOutputItemUnion) any {
	switch item.Type {
	case "web_search_call":
		result := map[string]any{
			"status": item.Status,
		}
		if action := toJSONObject(item.Action); len(action) > 0 {
			result["action"] = action
		}
		return result
	case "file_search_call":
		return map[string]any{
			"queries": item.Queries,
			"results": item.Results,
			"status":  item.Status,
		}
	case "code_interpreter_call":
		return map[string]any{
			"outputs":     item.Outputs,
			"status":      item.Status,
			"containerId": item.ContainerID,
		}
	case "image_generation_call":
		return map[string]any{
			"status": item.Status,
			"result": item.Result,
		}
	case "mcp_call":
		result := map[string]any{
			"type":        "call",
			"serverLabel": item.ServerLabel,
			"name":        item.Name,
			"arguments":   item.Arguments,
			"status":      item.Status,
		}
		if output := strings.TrimSpace(item.Output.OfString); output != "" {
			result["output"] = parseJSONOrRaw(output)
		}
		if strings.TrimSpace(item.Error) != "" {
			result["error"] = item.Error
		}
		return result
	case "mcp_list_tools":
		result := map[string]any{
			"serverLabel": item.ServerLabel,
			"tools":       item.Tools,
		}
		if strings.TrimSpace(item.Error) != "" {
			result["error"] = item.Error
		}
		return result
	case "shell_call_output":
		if output := item.Output.OfResponseFunctionShellToolCallOutputOutputArray; len(output) > 0 {
			return map[string]any{"output": output}
		}
		if output := strings.TrimSpace(item.Output.OfString); output != "" {
			return parseJSONOrRaw(output)
		}
		return responseOutputItemToMap(item)
	default:
		if mapped := responseOutputItemToMap(item); len(mapped) > 0 {
			return mapped
		}
		return map[string]any{"status": item.Status}
	}
}

func codeInterpreterFileParts(item responses.ResponseOutputItemUnion) []generatedFilePart {
	if item.Type != "code_interpreter_call" || len(item.Outputs) == 0 {
		return nil
	}
	files := make([]generatedFilePart, 0, len(item.Outputs))
	for _, output := range item.Outputs {
		image := output.AsImage()
		if strings.TrimSpace(image.URL) == "" {
			continue
		}
		files = append(files, generatedFilePart{
			url:       strings.TrimSpace(image.URL),
			mediaType: "image/png",
		})
	}
	return files
}

func responseMetadataDeltaFromResponse(resp responses.Response) map[string]any {
	metadata := map[string]any{}
	if strings.TrimSpace(resp.ID) != "" {
		metadata["response_id"] = resp.ID
	}
	if strings.TrimSpace(string(resp.Status)) != "" {
		metadata["response_status"] = string(resp.Status)
	}
	if strings.TrimSpace(string(resp.Model)) != "" {
		metadata["model"] = string(resp.Model)
	}
	return metadata
}

func (oc *AIClient) emitUIRuntimeMetadata(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	extra map[string]any,
) {
	base := oc.buildUIMessageMetadata(state, meta, false)
	if len(extra) > 0 {
		base = mergeMaps(base, extra)
	}
	oc.emitUIMessageMetadata(ctx, portal, state, base)
}

func (oc *AIClient) emitUIStart(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata) {
	if state.uiStarted {
		return
	}
	state.uiStarted = true
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":            "start",
		"messageId":       state.turnID,
		"messageMetadata": oc.buildUIMessageMetadata(state, meta, false),
	})
}

func (oc *AIClient) emitUIMessageMetadata(ctx context.Context, portal *bridgev2.Portal, state *streamingState, metadata map[string]any) {
	if len(metadata) == 0 {
		return
	}
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":            "message-metadata",
		"messageMetadata": metadata,
	})
}

func (oc *AIClient) emitUIAbort(ctx context.Context, portal *bridgev2.Portal, state *streamingState, reason string) {
	part := map[string]any{
		"type": "abort",
	}
	if strings.TrimSpace(reason) != "" {
		part["reason"] = reason
	}
	oc.emitStreamEvent(ctx, portal, state, part)
}

func (oc *AIClient) emitUIStepStart(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	if state.uiStepOpen {
		return
	}
	state.uiStepOpen = true
	state.uiStepCount++
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type": "start-step",
	})
}

func (oc *AIClient) emitUIStepFinish(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	if !state.uiStepOpen {
		return
	}
	state.uiStepOpen = false
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type": "finish-step",
	})
}

func (oc *AIClient) ensureUIText(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	if state.uiTextID != "" {
		return
	}
	state.uiTextID = fmt.Sprintf("text-%s", state.turnID)
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type": "text-start",
		"id":   state.uiTextID,
	})
}

func (oc *AIClient) ensureUIReasoning(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	if state.uiReasoningID != "" {
		return
	}
	state.uiReasoningID = fmt.Sprintf("reasoning-%s", state.turnID)
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type": "reasoning-start",
		"id":   state.uiReasoningID,
	})
}

func (oc *AIClient) emitUITextDelta(ctx context.Context, portal *bridgev2.Portal, state *streamingState, delta string) {
	oc.ensureUIText(ctx, portal, state)
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":  "text-delta",
		"id":    state.uiTextID,
		"delta": delta,
	})
}

func (oc *AIClient) emitUIReasoningDelta(ctx context.Context, portal *bridgev2.Portal, state *streamingState, delta string) {
	oc.ensureUIReasoning(ctx, portal, state)
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":  "reasoning-delta",
		"id":    state.uiReasoningID,
		"delta": delta,
	})
}

func (oc *AIClient) ensureUIToolInputStart(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	toolCallID string,
	toolName string,
	providerExecuted bool,
	dynamic bool,
	title string,
	providerMetadata map[string]any,
) {
	if toolCallID == "" {
		return
	}
	if !state.uiToolStarted[toolCallID] {
		state.uiToolStarted[toolCallID] = true
		if strings.TrimSpace(toolName) != "" {
			state.uiToolNameByToolCallID[toolCallID] = toolName
		}
		part := map[string]any{
			"type":             "tool-input-start",
			"toolCallId":       toolCallID,
			"toolName":         toolName,
			"providerExecuted": providerExecuted,
		}
		if dynamic {
			part["dynamic"] = true
		}
		if strings.TrimSpace(title) != "" {
			part["title"] = title
		}
		if len(providerMetadata) > 0 {
			part["providerMetadata"] = providerMetadata
		}
		oc.emitStreamEvent(ctx, portal, state, part)
	}
	if strings.TrimSpace(toolName) != "" {
		state.uiToolNameByToolCallID[toolCallID] = toolName
	}
}

func (oc *AIClient) emitUIToolInputDelta(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID, toolName, delta string, providerExecuted bool) {
	if toolCallID == "" {
		return
	}
	oc.ensureUIToolInputStart(ctx, portal, state, toolCallID, toolName, providerExecuted, false, "", nil)
	if delta != "" {
		oc.emitStreamEvent(ctx, portal, state, map[string]any{
			"type":           "tool-input-delta",
			"toolCallId":     toolCallID,
			"inputTextDelta": delta,
		})
	}
}

func (oc *AIClient) emitUIToolInputAvailable(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID, toolName string, input any, providerExecuted bool) {
	if toolCallID == "" {
		return
	}
	oc.ensureUIToolInputStart(ctx, portal, state, toolCallID, toolName, providerExecuted, false, "", nil)
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":             "tool-input-available",
		"toolCallId":       toolCallID,
		"toolName":         toolName,
		"input":            input,
		"providerExecuted": providerExecuted,
	})
}

func (oc *AIClient) emitUIToolInputError(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	toolCallID, toolName string,
	input any,
	errorText string,
	providerExecuted bool,
	dynamic bool,
) {
	if toolCallID == "" {
		return
	}
	oc.ensureUIToolInputStart(ctx, portal, state, toolCallID, toolName, providerExecuted, dynamic, "", nil)
	part := map[string]any{
		"type":             "tool-input-error",
		"toolCallId":       toolCallID,
		"toolName":         toolName,
		"input":            input,
		"errorText":        errorText,
		"providerExecuted": providerExecuted,
	}
	if dynamic {
		part["dynamic"] = true
	}
	oc.emitStreamEvent(ctx, portal, state, part)
}

func (oc *AIClient) emitUIToolApprovalRequest(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	approvalID string,
	toolCallID string,
	toolName string,
	targetEventID id.EventID,
	ttlSeconds int,
) {
	if strings.TrimSpace(approvalID) == "" || strings.TrimSpace(toolCallID) == "" {
		return
	}
	if state == nil {
		// Without a streaming state we can't track approvals or emit stream events safely.
		return
	}
	state.uiToolCallIDByApproval[approvalID] = toolCallID
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":       "tool-approval-request",
		"approvalId": approvalID,
		"toolCallId": toolCallID,
		"toolName":   toolName,
		"ttlSeconds": ttlSeconds,
	})

	// Back-compat fallback: many clients either don't support or don't render our
	// ephemeral stream events. If approvals are required, give the user a clear,
	// timeline-visible way to proceed (/approve or UI card).
	if state.suppressSend {
		return
	}
	if portal == nil || portal.MXID == "" {
		return
	}
	// Avoid spamming the timeline for the same approval. The ephemeral event is
	// still emitted above (so capable clients can render the native UI).
	if state.codexTimelineNotices == nil {
		state.codexTimelineNotices = make(map[string]bool)
	}
	noticeKey := "approval:" + approvalID
	if state.codexTimelineNotices[noticeKey] {
		return
	}
	state.codexTimelineNotices[noticeKey] = true

	if strings.TrimSpace(toolName) == "" {
		toolName = "tool"
	}
	mins := 0
	if ttlSeconds > 0 {
		mins = (ttlSeconds + 59) / 60
	}
	expires := ""
	if mins > 0 {
		expires = fmt.Sprintf(" Expires in %d min.", mins)
	}
	body := fmt.Sprintf(
		"Approval required to run %s. Type /approve %s allow|always|deny.%s",
		toolName,
		approvalID,
		expires,
	)
	expiresAtMs := int64(0)
	if ttlSeconds > 0 {
		expiresAtMs = time.Now().Add(time.Duration(ttlSeconds) * time.Second).UnixMilli()
	}

	uiMessage := map[string]any{
		"id":   "approval:" + approvalID,
		"role": "assistant",
		"metadata": map[string]any{
			"turn_id":      state.turnID,
			"approval_id":  approvalID,
			"tool_call_id": toolCallID,
			// Allows clients to disable the UI locally even if the snapshot isn't edited on timeout.
			"approval_expires_at_ms": expiresAtMs,
		},
		"parts": []map[string]any{
			{
				"type":       "dynamic-tool",
				"toolName":   toolName,
				"toolCallId": toolCallID,
				"state":      "approval-requested",
				"approval": map[string]any{
					"id":          approvalID,
					"expiresAtMs": expiresAtMs,
				},
			},
		},
	}

	raw := map[string]any{
		"body":      body,
		"msgtype":   event.MsgNotice,
		BeeperAIKey: uiMessage,
	}
	if targetEventID != "" {
		raw["m.relates_to"] = map[string]any{
			"rel_type": RelReference,
			"event_id": targetEventID.String(),
		}
	}
	content := &event.Content{Raw: raw}

	// Prefer sending as the model/assistant identity if possible (so the message
	// reads as part of the assistant's flow), but fall back to the bridge bot.
	if intent := oc.getModelIntent(ctx, portal); intent != nil {
		if resp, err := intent.SendMessage(ctx, portal.MXID, event.EventMessage, content, nil); err == nil {
			if resp != nil && resp.EventID != "" {
				oc.setApprovalSnapshotEvent(approvalID, resp.EventID, false)
			}
			return
		}
	}
	if oc != nil && oc.UserLogin != nil && oc.UserLogin.Bridge != nil && oc.UserLogin.Bridge.Bot != nil {
		if resp, err := oc.UserLogin.Bridge.Bot.SendMessage(ctx, portal.MXID, event.EventMessage, content, nil); err == nil {
			if resp != nil && resp.EventID != "" {
				oc.setApprovalSnapshotEvent(approvalID, resp.EventID, true)
			}
		}
	}
}

func (oc *AIClient) emitUIToolOutputAvailable(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID string, output any, providerExecuted bool, preliminary bool) {
	if toolCallID == "" {
		return
	}
	if state != nil && !preliminary {
		if state.uiToolOutputFinalized[toolCallID] {
			return
		}
		state.uiToolOutputFinalized[toolCallID] = true
	}
	part := map[string]any{
		"type":             "tool-output-available",
		"toolCallId":       toolCallID,
		"output":           output,
		"providerExecuted": providerExecuted,
	}
	if preliminary {
		part["preliminary"] = true
	}
	oc.emitStreamEvent(ctx, portal, state, part)
}

func (oc *AIClient) emitUIToolOutputDenied(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID string) {
	if strings.TrimSpace(toolCallID) == "" {
		return
	}
	if state != nil {
		if state.uiToolOutputFinalized[toolCallID] {
			return
		}
		state.uiToolOutputFinalized[toolCallID] = true
	}
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":       "tool-output-denied",
		"toolCallId": toolCallID,
	})
}

func (oc *AIClient) emitUIToolOutputError(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID, errorText string, providerExecuted bool) {
	if toolCallID == "" {
		return
	}
	if state != nil {
		if state.uiToolOutputFinalized[toolCallID] {
			return
		}
		state.uiToolOutputFinalized[toolCallID] = true
	}
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":             "tool-output-error",
		"toolCallId":       toolCallID,
		"errorText":        errorText,
		"providerExecuted": providerExecuted,
	})
}

func (oc *AIClient) emitUISourceURL(ctx context.Context, portal *bridgev2.Portal, state *streamingState, citation sourceCitation) {
	if state == nil {
		return
	}
	url := strings.TrimSpace(citation.URL)
	if url == "" {
		return
	}
	if state.uiSourceURLSeen[url] {
		return
	}
	state.uiSourceURLSeen[url] = true
	part := map[string]any{
		"type":     "source-url",
		"sourceId": fmt.Sprintf("source-url-%d", len(state.uiSourceURLSeen)),
		"url":      url,
	}
	if title := strings.TrimSpace(citation.Title); title != "" {
		part["title"] = title
	}
	providerMeta := map[string]any{}
	if desc := strings.TrimSpace(citation.Description); desc != "" {
		providerMeta["description"] = desc
	}
	if published := strings.TrimSpace(citation.Published); published != "" {
		providerMeta["published"] = published
	}
	if site := strings.TrimSpace(citation.SiteName); site != "" {
		providerMeta["site_name"] = site
	}
	if author := strings.TrimSpace(citation.Author); author != "" {
		providerMeta["author"] = author
	}
	if image := strings.TrimSpace(citation.Image); image != "" {
		providerMeta["image"] = image
	}
	if favicon := strings.TrimSpace(citation.Favicon); favicon != "" {
		providerMeta["favicon"] = favicon
	}
	if len(providerMeta) > 0 {
		part["providerMetadata"] = providerMeta
	}
	oc.emitStreamEvent(ctx, portal, state, part)
}

func (oc *AIClient) emitUISourceDocument(ctx context.Context, portal *bridgev2.Portal, state *streamingState, doc sourceDocument) {
	if state == nil {
		return
	}
	key := strings.TrimSpace(doc.ID)
	if key == "" {
		key = strings.TrimSpace(doc.Filename)
	}
	if key == "" {
		key = strings.TrimSpace(doc.Title)
	}
	if key == "" {
		return
	}
	if state.uiSourceDocumentSeen[key] {
		return
	}
	state.uiSourceDocumentSeen[key] = true
	part := map[string]any{
		"type":      "source-document",
		"sourceId":  fmt.Sprintf("source-doc-%d", len(state.uiSourceDocumentSeen)),
		"mediaType": strings.TrimSpace(doc.MediaType),
		"title":     strings.TrimSpace(doc.Title),
	}
	if part["mediaType"] == "" {
		part["mediaType"] = "application/octet-stream"
	}
	if title, _ := part["title"].(string); title == "" {
		part["title"] = key
	}
	if filename := strings.TrimSpace(doc.Filename); filename != "" {
		part["filename"] = filename
	}
	oc.emitStreamEvent(ctx, portal, state, part)
}

func (oc *AIClient) emitUIFile(ctx context.Context, portal *bridgev2.Portal, state *streamingState, fileURL, mediaType string) {
	if state == nil {
		return
	}
	fileURL = strings.TrimSpace(fileURL)
	if fileURL == "" {
		return
	}
	if state.uiFileSeen[fileURL] {
		return
	}
	state.uiFileSeen[fileURL] = true
	if strings.TrimSpace(mediaType) == "" {
		mediaType = "application/octet-stream"
	}
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":      "file",
		"url":       fileURL,
		"mediaType": mediaType,
	})
}

func (oc *AIClient) upsertActiveToolFromDescriptor(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools map[string]*activeToolCall,
	desc responseToolDescriptor,
) *activeToolCall {
	if activeTools == nil || strings.TrimSpace(desc.itemID) == "" || strings.TrimSpace(desc.callID) == "" {
		return nil
	}
	tool, ok := activeTools[desc.itemID]
	if !ok || tool == nil {
		tool = &activeToolCall{
			callID:      SanitizeToolCallID(desc.callID, "strict"),
			toolName:    desc.toolName,
			toolType:    desc.toolType,
			startedAtMs: time.Now().UnixMilli(),
			itemID:      desc.itemID,
		}
		activeTools[desc.itemID] = tool
	}
	if strings.TrimSpace(desc.callID) != "" {
		tool.callID = SanitizeToolCallID(desc.callID, "strict")
	}
	if strings.TrimSpace(desc.toolName) != "" {
		tool.toolName = desc.toolName
	}
	if desc.toolType != "" {
		tool.toolType = desc.toolType
	}
	state.uiToolNameByToolCallID[tool.callID] = tool.toolName
	state.uiToolTypeByToolCallID[tool.callID] = tool.toolType

	if tool.eventID == "" && strings.TrimSpace(tool.toolName) != "" {
		tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
	}
	oc.ensureUIToolInputStart(ctx, portal, state, tool.callID, tool.toolName, desc.providerExecuted, desc.dynamic, "", nil)
	return tool
}

func (oc *AIClient) handleResponseOutputItemAdded(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools map[string]*activeToolCall,
	item responses.ResponseOutputItemUnion,
) {
	desc := deriveToolDescriptorForOutputItem(item, state)
	if !desc.ok {
		return
	}
	// Keep legacy handlers for function/web/image to avoid duplicate tool events.
	switch item.Type {
	case "function_call", "web_search_call", "image_generation_call":
		return
	}
	if state == nil {
		return
	}
	tool := oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, desc)
	if tool == nil {
		return
	}
	if state.uiToolOutputFinalized[tool.callID] {
		return
	}

	if item.Type == "mcp_approval_request" {
		approvalID := strings.TrimSpace(item.ID)
		if approvalID == "" {
			approvalID = NewCallID()
		}
		state.uiToolCallIDByApproval[approvalID] = tool.callID
		if tool.input.Len() == 0 {
			tool.input.WriteString(stringifyJSONValue(desc.input))
		}
		oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, desc.input, true)
		if !state.pendingMcpApprovalsSeen[approvalID] {
			state.pendingMcpApprovalsSeen[approvalID] = true
			parsed := item.AsMcpApprovalRequest()
			serverLabel := strings.TrimSpace(parsed.ServerLabel)
			mcpToolName := strings.TrimSpace(parsed.Name)
			state.pendingMcpApprovals = append(state.pendingMcpApprovals, mcpApprovalRequest{
				approvalID:  approvalID,
				toolCallID:  tool.callID,
				toolName:    tool.toolName,
				serverLabel: serverLabel,
			})
			ttl := time.Duration(oc.toolApprovalsTTLSeconds()) * time.Second
			oc.registerToolApproval(struct {
				ApprovalID string
				RoomID     id.RoomID
				TurnID     string

				ToolCallID string
				ToolName   string

				ToolKind     ToolApprovalKind
				RuleToolName string
				ServerLabel  string
				Action       string

				TTL time.Duration
			}{
				ApprovalID:   approvalID,
				RoomID:       state.roomID,
				TurnID:       state.turnID,
				ToolCallID:   tool.callID,
				ToolName:     tool.toolName,
				ToolKind:     ToolApprovalKindMCP,
				RuleToolName: mcpToolName,
				ServerLabel:  serverLabel,
				TTL:          ttl,
			})

			// If approvals are disabled, not required, or already always-allowed, auto-approve without prompting.
			// Otherwise emit an approval request to the UI.
			needsApproval := oc.toolApprovalsRuntimeEnabled() && oc.toolApprovalsRequireForMCP() && !oc.isMcpAlwaysAllowed(serverLabel, mcpToolName)
			if needsApproval && state != nil && state.heartbeat != nil {
				needsApproval = false
			}
			if needsApproval {
				if state != nil && !state.uiToolApprovalRequested[approvalID] {
					state.uiToolApprovalRequested[approvalID] = true
					oc.emitUIToolApprovalRequest(ctx, portal, state, approvalID, tool.callID, tool.toolName, tool.eventID, oc.toolApprovalsTTLSeconds())
				}
			} else {
				_ = oc.resolveToolApproval(state.roomID, approvalID, ToolApprovalDecision{
					Approve:   true,
					DecidedAt: time.Now(),
					DecidedBy: oc.UserLogin.UserMXID,
				})
			}
		}
		return
	}

	if desc.input != nil {
		if tool.input.Len() == 0 {
			tool.input.WriteString(stringifyJSONValue(desc.input))
		}
		oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, desc.input, desc.providerExecuted)
	}
}

func (oc *AIClient) handleResponseOutputItemDone(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	activeTools map[string]*activeToolCall,
	item responses.ResponseOutputItemUnion,
) {
	desc := deriveToolDescriptorForOutputItem(item, state)
	if !desc.ok {
		return
	}
	// Keep legacy handlers for function/web/image to avoid duplicate tool events.
	switch item.Type {
	case "function_call", "web_search_call", "image_generation_call":
		return
	}
	if state == nil {
		return
	}
	tool := oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, desc)
	if tool == nil {
		return
	}
	if state.uiToolOutputFinalized[tool.callID] {
		return
	}

	if item.Type == "mcp_approval_request" {
		approvalID := strings.TrimSpace(item.ID)
		if approvalID == "" {
			approvalID = NewCallID()
		}
		state.uiToolCallIDByApproval[approvalID] = tool.callID
		if tool.input.Len() == 0 {
			tool.input.WriteString(stringifyJSONValue(desc.input))
		}
		oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, desc.input, true)
		if !state.pendingMcpApprovalsSeen[approvalID] {
			state.pendingMcpApprovalsSeen[approvalID] = true
			parsed := item.AsMcpApprovalRequest()
			serverLabel := strings.TrimSpace(parsed.ServerLabel)
			mcpToolName := strings.TrimSpace(parsed.Name)
			state.pendingMcpApprovals = append(state.pendingMcpApprovals, mcpApprovalRequest{
				approvalID:  approvalID,
				toolCallID:  tool.callID,
				toolName:    tool.toolName,
				serverLabel: serverLabel,
			})
			ttl := time.Duration(oc.toolApprovalsTTLSeconds()) * time.Second
			oc.registerToolApproval(struct {
				ApprovalID string
				RoomID     id.RoomID
				TurnID     string

				ToolCallID string
				ToolName   string

				ToolKind     ToolApprovalKind
				RuleToolName string
				ServerLabel  string
				Action       string

				TTL time.Duration
			}{
				ApprovalID:   approvalID,
				RoomID:       state.roomID,
				TurnID:       state.turnID,
				ToolCallID:   tool.callID,
				ToolName:     tool.toolName,
				ToolKind:     ToolApprovalKindMCP,
				RuleToolName: mcpToolName,
				ServerLabel:  serverLabel,
				TTL:          ttl,
			})

			needsApproval := oc.toolApprovalsRuntimeEnabled() && oc.toolApprovalsRequireForMCP() && !oc.isMcpAlwaysAllowed(serverLabel, mcpToolName)
			if needsApproval && state != nil && state.heartbeat != nil {
				needsApproval = false
			}
			if needsApproval {
				if state != nil && !state.uiToolApprovalRequested[approvalID] {
					state.uiToolApprovalRequested[approvalID] = true
					oc.emitUIToolApprovalRequest(ctx, portal, state, approvalID, tool.callID, tool.toolName, tool.eventID, oc.toolApprovalsTTLSeconds())
				}
			} else {
				_ = oc.resolveToolApproval(state.roomID, approvalID, ToolApprovalDecision{
					Approve:   true,
					DecidedAt: time.Now(),
					DecidedBy: oc.UserLogin.UserMXID,
				})
			}
		}
		return
	}

	if desc.input != nil {
		if tool.input.Len() == 0 {
			tool.input.WriteString(stringifyJSONValue(desc.input))
		}
		oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, desc.input, desc.providerExecuted)
	}

	if files := codeInterpreterFileParts(item); len(files) > 0 {
		for _, file := range files {
			recordGeneratedFile(state, file.url, file.mediaType)
			oc.emitUIFile(ctx, portal, state, file.url, file.mediaType)
		}
	}

	result := responseOutputItemResultPayload(item)
	resultStatus := ResultStatusSuccess
	statusText := strings.ToLower(strings.TrimSpace(item.Status))
	errorText := strings.TrimSpace(item.Error)
	switch {
	case outputItemLooksDenied(item):
		oc.emitUIToolOutputDenied(ctx, portal, state, tool.callID)
		resultStatus = ResultStatusDenied
	case statusText == "failed" || statusText == "incomplete" || errorText != "":
		if errorText == "" {
			errorText = fmt.Sprintf("%s failed", tool.toolName)
		}
		oc.emitUIToolOutputError(ctx, portal, state, tool.callID, errorText, true)
		resultStatus = ResultStatusError
	default:
		oc.emitUIToolOutputAvailable(ctx, portal, state, tool.callID, result, true, false)
	}

	resultJSON, _ := json.Marshal(result)
	resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, string(resultJSON), resultStatus)
	outputMap := map[string]any{}
	if converted := toJSONObject(result); len(converted) > 0 {
		outputMap = converted
	} else if result != nil {
		outputMap = map[string]any{"result": result}
	}

	state.toolCalls = append(state.toolCalls, ToolCallMetadata{
		CallID:        tool.callID,
		ToolName:      tool.toolName,
		ToolType:      string(tool.toolType),
		Input:         parseToolInputPayload(tool.input.String()),
		Output:        outputMap,
		Status:        string(ToolStatusCompleted),
		ResultStatus:  string(resultStatus),
		ErrorMessage:  errorText,
		StartedAtMs:   tool.startedAtMs,
		CompletedAtMs: time.Now().UnixMilli(),
		CallEventID:   string(tool.eventID),
		ResultEventID: string(resultEventID),
	})
}

func collectToolOutputCitations(state *streamingState, toolName, output string) {
	if state == nil {
		return
	}
	citations := extractWebSearchCitationsFromToolOutput(toolName, output)
	if len(citations) == 0 {
		return
	}
	state.sourceCitations = mergeSourceCitations(state.sourceCitations, citations)
}

func (oc *AIClient) emitUIError(ctx context.Context, portal *bridgev2.Portal, state *streamingState, errText string) {
	if errText == "" {
		errText = "Unknown error"
	}
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":      "error",
		"errorText": errText,
	})
}

func (oc *AIClient) emitUIFinish(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata) {
	if state.uiFinished {
		return
	}
	state.uiFinished = true
	if state.uiTextID != "" {
		oc.emitStreamEvent(ctx, portal, state, map[string]any{
			"type": "text-end",
			"id":   state.uiTextID,
		})
		state.uiTextID = ""
	}
	if state.uiReasoningID != "" {
		oc.emitStreamEvent(ctx, portal, state, map[string]any{
			"type": "reasoning-end",
			"id":   state.uiReasoningID,
		})
		state.uiReasoningID = ""
	}
	oc.emitUIStepFinish(ctx, portal, state)
	// Finalize any un-finished tool calls before sending finish.
	// If a stream ends (error, cancel, timeout) while a tool is mid-execution,
	// these tools would otherwise stay in a non-terminal state forever.
	for toolCallID := range state.uiToolStarted {
		if !state.uiToolOutputFinalized[toolCallID] {
			oc.emitUIToolOutputError(ctx, portal, state, toolCallID, "cancelled", false)
		}
	}
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":            "finish",
		"finishReason":    mapFinishReason(state.finishReason),
		"messageMetadata": oc.buildUIMessageMetadata(state, meta, true),
	})

	// Debounced done summary: always log the finish with event count.
	if state.loggedStreamStart {
		oc.loggerForContext(ctx).Info().
			Str("turn_id", strings.TrimSpace(state.turnID)).
			Int("events_sent", state.sequenceNum).
			Msg("Finished streaming ephemeral events")
	}
}

// saveAssistantMessage saves the completed assistant message to the database
func (oc *AIClient) saveAssistantMessage(
	ctx context.Context,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
) {
	modelID := oc.effectiveModel(meta)
	assistantMsg := &database.Message{
		ID:        MakeMessageID(state.initialEventID),
		Room:      portal.PortalKey,
		SenderID:  modelUserID(modelID),
		MXID:      state.initialEventID,
		Timestamp: time.Now(),
		Metadata: &MessageMetadata{
			Role:               "assistant",
			Body:               state.accumulated.String(),
			CompletionID:       state.responseID,
			FinishReason:       state.finishReason,
			Model:              modelID,
			TurnID:             state.turnID,
			AgentID:            state.agentID,
			ToolCalls:          state.toolCalls,
			StartedAtMs:        state.startedAtMs,
			FirstTokenAtMs:     state.firstTokenAtMs,
			CompletedAtMs:      state.completedAtMs,
			HasToolCalls:       len(state.toolCalls) > 0,
			CanonicalSchema:    "ai-sdk-ui-message-v1",
			CanonicalUIMessage: oc.buildCanonicalUIMessage(state, meta),
			// Reasoning fields (only populated by Responses API)
			ThinkingContent:    state.reasoning.String(),
			ThinkingTokenCount: len(strings.Fields(state.reasoning.String())),
			PromptTokens:       state.promptTokens,
			CompletionTokens:   state.completionTokens,
			ReasoningTokens:    state.reasoningTokens,
		},
	}
	if err := oc.UserLogin.Bridge.DB.Message.Insert(ctx, assistantMsg); err != nil {
		log.Warn().Err(err).Msg("Failed to save assistant message to database")
	} else {
		log.Debug().Str("msg_id", string(assistantMsg.ID)).Msg("Saved assistant message to database")
	}
	oc.notifySessionMemoryChange(ctx, portal, meta, false)

	// Save LastResponseID for "responses" mode context chaining (OpenAI-only)
	if meta.ConversationMode == "responses" && state.responseID != "" && !oc.isOpenRouterProvider() {
		meta.LastResponseID = state.responseID
		if err := portal.Save(ctx); err != nil {
			log.Warn().Err(err).Msg("Failed to save portal after storing response ID")
		}
	}
}

func (oc *AIClient) buildCanonicalUIMessage(state *streamingState, meta *PortalMetadata) map[string]any {
	if state == nil {
		return nil
	}

	parts := make([]map[string]any, 0, 2+len(state.toolCalls))
	reasoningText := strings.TrimSpace(state.reasoning.String())
	if reasoningText != "" {
		parts = append(parts, map[string]any{
			"type":  "reasoning",
			"text":  reasoningText,
			"state": "done",
		})
	}
	text := state.accumulated.String()
	if text != "" {
		parts = append(parts, map[string]any{
			"type":  "text",
			"text":  text,
			"state": "done",
		})
	}
	for _, tc := range state.toolCalls {
		toolPart := map[string]any{
			"type":       "dynamic-tool",
			"toolName":   tc.ToolName,
			"toolCallId": tc.CallID,
			"input":      tc.Input,
		}
		if tc.ToolType == string(ToolTypeProvider) {
			toolPart["providerExecuted"] = true
		}
		if tc.ResultStatus == string(ResultStatusSuccess) {
			toolPart["state"] = "output-available"
			toolPart["output"] = tc.Output
		} else {
			toolPart["state"] = "output-error"
			if tc.ErrorMessage != "" {
				toolPart["errorText"] = tc.ErrorMessage
			} else if result, ok := tc.Output["result"].(string); ok && result != "" {
				toolPart["errorText"] = result
			}
		}
		parts = append(parts, toolPart)
	}
	if sourceParts := buildSourceParts(state.sourceCitations, state.sourceDocuments, nil); len(sourceParts) > 0 {
		parts = append(parts, sourceParts...)
	}
	if fileParts := generatedFilesToParts(state.generatedFiles); len(fileParts) > 0 {
		parts = append(parts, fileParts...)
	}

	messageID := state.turnID
	if strings.TrimSpace(messageID) == "" && state.initialEventID != "" {
		messageID = state.initialEventID.String()
	}

	metadata := map[string]any{}
	if state.turnID != "" {
		metadata["turn_id"] = state.turnID
	}
	if state.agentID != "" {
		metadata["agent_id"] = state.agentID
	}
	if model := oc.effectiveModel(meta); model != "" {
		metadata["model"] = model
	}
	if state.finishReason != "" {
		metadata["finish_reason"] = mapFinishReason(state.finishReason)
	}
	if state.promptTokens > 0 || state.completionTokens > 0 || state.reasoningTokens > 0 {
		metadata["usage"] = map[string]any{
			"prompt_tokens":     state.promptTokens,
			"completion_tokens": state.completionTokens,
			"reasoning_tokens":  state.reasoningTokens,
		}
	}
	timing := map[string]any{}
	if state.startedAtMs > 0 {
		timing["started_at"] = state.startedAtMs
	}
	if state.firstTokenAtMs > 0 {
		timing["first_token_at"] = state.firstTokenAtMs
	}
	if state.completedAtMs > 0 {
		timing["completed_at"] = state.completedAtMs
	}
	if len(timing) > 0 {
		metadata["timing"] = timing
	}

	uiMessage := map[string]any{
		"id":    messageID,
		"role":  "assistant",
		"parts": parts,
	}
	if len(metadata) > 0 {
		uiMessage["metadata"] = metadata
	}
	return uiMessage
}

// buildResponsesAPIParams creates common Responses API parameters for both streaming and non-streaming paths
func (oc *AIClient) buildResponsesAPIParams(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, messages []openai.ChatCompletionMessageParamUnion) responses.ResponseNewParams {
	log := zerolog.Ctx(ctx)

	params := responses.ResponseNewParams{
		Model:           shared.ResponsesModel(oc.effectiveModelForAPI(meta)),
		MaxOutputTokens: openai.Int(int64(oc.effectiveMaxTokens(meta))),
	}

	systemPrompt := oc.effectivePrompt(meta)

	// Use previous_response_id if in "responses" mode and ID exists.
	// OpenRouter's Responses API is stateless, so always send full history there.
	usePreviousResponse := meta.ConversationMode == "responses" && meta.LastResponseID != "" && !oc.isOpenRouterProvider()
	if usePreviousResponse {
		params.PreviousResponseID = openai.String(meta.LastResponseID)
		if systemPrompt != "" {
			params.Instructions = openai.String(systemPrompt)
		}
		// Still need to pass the latest user message as input
		if len(messages) > 0 {
			latestMsg := messages[len(messages)-1]
			input := oc.convertToResponsesInput([]openai.ChatCompletionMessageParamUnion{latestMsg}, meta)
			params.Input = responses.ResponseNewParamsInputUnion{
				OfInputItemList: input,
			}
		}
		log.Debug().Str("previous_response_id", meta.LastResponseID).Msg("Using previous_response_id for context")
	} else {
		// Build full message history
		input := oc.convertToResponsesInput(messages, meta)
		params.Input = responses.ResponseNewParamsInputUnion{
			OfInputItemList: input,
		}
	}

	// Add reasoning effort if configured (uses inheritance: room → user → default)
	if reasoningEffort := oc.effectiveReasoningEffort(meta); reasoningEffort != "" {
		params.Reasoning = shared.ReasoningParam{
			Effort: shared.ReasoningEffort(reasoningEffort),
		}
	}

	// OpenRouter's Responses API only supports function-type tools.
	isOpenRouter := oc.isOpenRouterProvider()
	log.Debug().
		Bool("is_openrouter", isOpenRouter).
		Str("detected_provider", loginMetadata(oc.UserLogin).Provider).
		Msg("Provider detection for tool filtering")

	// Add builtin function tools only for agent chats that support tool calling.
	// Model-only chats use a simple prompt without tools to avoid context overflow on small models.
	hasAgent := resolveAgentID(meta) != ""
	if meta.Capabilities.SupportsToolCalling && hasAgent {
		enabledTools := oc.enabledBuiltinToolsForModel(ctx, meta)
		if len(enabledTools) > 0 {
			strictMode := resolveToolStrictMode(oc.isOpenRouterProvider())
			params.Tools = append(params.Tools, ToOpenAITools(enabledTools, strictMode, &oc.log)...)
			log.Debug().Int("count", len(enabledTools)).Msg("Added builtin function tools")
		}

		// Add session tools for non-boss rooms
		if !hasBossAgent(meta) && !oc.isBuilderRoom(portal) {
			var enabledSessions []*tools.Tool
			for _, tool := range tools.SessionTools() {
				if oc.isToolEnabled(meta, tool.Name) {
					enabledSessions = append(enabledSessions, tool)
				}
			}
			if len(enabledSessions) > 0 {
				strictMode := resolveToolStrictMode(oc.isOpenRouterProvider())
				params.Tools = append(params.Tools, bossToolsToOpenAI(enabledSessions, strictMode, &oc.log)...)
				log.Debug().Int("count", len(enabledSessions)).Msg("Added session tools")
			}
		}
	}

	// Add boss tools if this is a Boss room
	if hasBossAgent(meta) || oc.isBuilderRoom(portal) {
		var enabledBoss []*tools.Tool
		for _, tool := range tools.BossTools() {
			if oc.isToolEnabled(meta, tool.Name) {
				enabledBoss = append(enabledBoss, tool)
			}
		}
		strictMode := resolveToolStrictMode(oc.isOpenRouterProvider())
		params.Tools = append(params.Tools, bossToolsToOpenAI(enabledBoss, strictMode, &oc.log)...)
		log.Debug().Int("count", len(enabledBoss)).Msg("Added boss agent tools")
	}

	if oc.isOpenRouterProvider() {
		params.Tools = renameWebSearchToolParams(params.Tools)
	}

	// Prevent duplicate tool names (Anthropic rejects duplicates)
	params.Tools = dedupeToolParams(params.Tools)
	logToolParamDuplicates(log, params.Tools)

	return params
}

// bossToolsToOpenAI converts boss tools to OpenAI Responses API format.
func bossToolsToOpenAI(bossTools []*tools.Tool, strictMode ToolStrictMode, log *zerolog.Logger) []responses.ToolUnionParam {
	var result []responses.ToolUnionParam
	for _, t := range bossTools {
		var schema map[string]any
		switch v := t.InputSchema.(type) {
		case nil:
			schema = nil
		case map[string]any:
			schema = v
		default:
			encoded, err := json.Marshal(v)
			if err == nil {
				if err := json.Unmarshal(encoded, &schema); err != nil {
					schema = nil
				}
			}
		}
		if schema != nil {
			var stripped []string
			schema, stripped = sanitizeToolSchemaWithReport(schema)
			logSchemaSanitization(log, t.Name, stripped)
		}
		strict := shouldUseStrictMode(strictMode, schema)
		toolParam := responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:       t.Name,
				Parameters: schema,
				Strict:     param.NewOpt(strict),
				Type:       constant.ValueOf[constant.Function](),
			},
		}
		if t.Description != "" && toolParam.OfFunction != nil {
			toolParam.OfFunction.Description = openai.String(t.Description)
		}
		result = append(result, toolParam)
	}
	return result
}

// bossToolsToChatTools converts boss tools to OpenAI Chat Completions tool format.
func bossToolsToChatTools(bossTools []*tools.Tool, log *zerolog.Logger) []openai.ChatCompletionToolUnionParam {
	var result []openai.ChatCompletionToolUnionParam
	for _, t := range bossTools {
		var schema map[string]any
		switch v := t.InputSchema.(type) {
		case nil:
			schema = nil
		case map[string]any:
			schema = v
		default:
			encoded, err := json.Marshal(v)
			if err == nil {
				if err := json.Unmarshal(encoded, &schema); err != nil {
					schema = nil
				}
			}
		}
		if schema != nil {
			var stripped []string
			schema, stripped = sanitizeToolSchemaWithReport(schema)
			logSchemaSanitization(log, t.Name, stripped)
		}
		function := openai.FunctionDefinitionParam{
			Name:       t.Name,
			Parameters: schema,
		}
		if t.Description != "" {
			function.Description = openai.String(t.Description)
		}
		result = append(result, openai.ChatCompletionToolUnionParam{
			OfFunction: &openai.ChatCompletionFunctionToolParam{
				Function: function,
				Type:     constant.ValueOf[constant.Function](),
			},
		})
	}
	return result
}

// streamingResponseWithToolSchemaFallback retries via Chat Completions when the provider
// rejects tool schemas in the Responses API.
func (oc *AIClient) streamingResponseWithToolSchemaFallback(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	messages []openai.ChatCompletionMessageParamUnion,
) (bool, *ContextLengthError, error) {
	success, cle, err := oc.streamingResponse(ctx, evt, portal, meta, messages)
	if success || cle != nil || err == nil {
		return success, cle, err
	}
	if IsToolUniquenessError(err) {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Duplicate tool names rejected; retrying with chat completions")
		success, cle, chatErr := oc.streamChatCompletions(ctx, evt, portal, meta, messages)
		if success || cle != nil || chatErr == nil {
			return success, cle, chatErr
		}
		if IsToolSchemaError(chatErr) || IsToolUniquenessError(chatErr) {
			oc.loggerForContext(ctx).Warn().Err(chatErr).Msg("Chat completions tools rejected; retrying without tools")
			if meta != nil {
				metaCopy := *meta
				metaCopy.Capabilities = meta.Capabilities
				metaCopy.Capabilities.SupportsToolCalling = false
				return oc.streamChatCompletions(ctx, evt, portal, &metaCopy, messages)
			}
		}
		return success, cle, chatErr
	}
	if IsToolSchemaError(err) {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Responses tool schema rejected; falling back to chat completions")
		success, cle, chatErr := oc.streamChatCompletions(ctx, evt, portal, meta, messages)
		if success || cle != nil || chatErr == nil {
			return success, cle, chatErr
		}
		if IsToolSchemaError(chatErr) {
			oc.loggerForContext(ctx).Warn().Err(chatErr).Msg("Chat completions tool schema rejected; retrying without tools")
			if meta != nil {
				metaCopy := *meta
				metaCopy.Capabilities = meta.Capabilities
				metaCopy.Capabilities.SupportsToolCalling = false
				return oc.streamChatCompletions(ctx, evt, portal, &metaCopy, messages)
			}
		}
		return success, cle, chatErr
	}
	if IsNoResponseChunksError(err) {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Responses streaming returned no chunks; retrying without tools")
		if meta != nil && meta.Capabilities.SupportsToolCalling {
			metaCopy := *meta
			metaCopy.Capabilities = meta.Capabilities
			metaCopy.Capabilities.SupportsToolCalling = false
			success, cle, retryErr := oc.streamingResponse(ctx, evt, portal, &metaCopy, messages)
			if success || cle != nil || retryErr == nil {
				return success, cle, retryErr
			}
			err = retryErr
		}
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Responses retry failed; falling back to chat completions")
		return oc.streamChatCompletions(ctx, evt, portal, meta, messages)
	}
	return success, cle, err
}

// streamingResponse handles streaming using the Responses API
// This is the preferred streaming method as it supports reasoning tokens
// Returns (success, contextLengthError)
func (oc *AIClient) streamingResponse(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	messages []openai.ChatCompletionMessageParamUnion,
) (bool, *ContextLengthError, error) {
	portalID := ""
	if portal != nil {
		portalID = string(portal.ID)
	}
	log := zerolog.Ctx(ctx).With().
		Str("portal_id", portalID).
		Logger()
	maxToolRounds := 3

	// Initialize streaming state with turn tracking
	// Pass source event ID for [[reply_to_current]] directive support
	var sourceEventID id.EventID
	senderID := ""
	if evt != nil {
		sourceEventID = evt.ID
		if evt.Sender != "" {
			senderID = evt.Sender.String()
		}
	}
	roomID := id.RoomID("")
	if portal != nil {
		roomID = portal.MXID
	}
	state := newStreamingState(ctx, meta, sourceEventID, senderID, roomID)
	state.replyTarget = oc.resolveInitialReplyTarget(evt)
	if state.roomID != "" {
		oc.markRoomRunStreaming(state.roomID, true)
		defer oc.markRoomRunStreaming(state.roomID, false)
	}

	// Ensure model ghost is in the room before any operations
	if !state.suppressSend {
		if err := oc.ensureModelInRoom(ctx, portal); err != nil {
			log.Warn().Err(err).Msg("Failed to ensure model is in room")
			// Continue anyway - typing will fail gracefully
		}
	}

	// Create typing controller with TTL and automatic refresh
	var typingCtrl *TypingController
	var typingSignals *TypingSignaler
	touchTyping := func() {}
	isHeartbeat := state.heartbeat != nil
	if !state.suppressSend && !isHeartbeat {
		mode := oc.resolveTypingMode(meta, typingContextFromContext(ctx), isHeartbeat)
		interval := oc.resolveTypingInterval(meta)
		if interval > 0 && mode != TypingModeNever {
			typingCtrl = NewTypingController(oc, ctx, portal, TypingControllerOptions{
				Interval: interval,
				TTL:      typingTTL,
			})
			typingSignals = NewTypingSignaler(typingCtrl, mode, isHeartbeat)
			touchTyping = func() {
				typingCtrl.RefreshTTL()
			}
		}
	}
	if typingSignals != nil {
		typingSignals.SignalRunStart()
	}
	defer func() {
		if typingCtrl != nil {
			typingCtrl.MarkRunComplete()
			typingCtrl.MarkDispatchIdle()
		}
	}()

	// Apply proactive context pruning if enabled
	messages = oc.applyProactivePruning(ctx, messages, meta)

	// Build Responses API params using shared helper
	params := oc.buildResponsesAPIParams(ctx, portal, meta, messages)

	// Inject per-room PDF engine into context for OpenRouter/Beeper providers
	if oc.isOpenRouterProvider() {
		ctx = WithPDFEngine(ctx, oc.effectivePDFEngine(meta))
	}

	stream := oc.api.Responses.NewStreaming(ctx, params)
	if stream == nil {
		initErr := errors.New("responses streaming not available")
		logResponsesFailure(log, initErr, params, meta, messages, "stream_init")
		return false, nil, &PreDeltaError{Err: initErr}
	}

	// Store base input for OpenRouter stateless continuations
	if params.Input.OfInputItemList != nil {
		state.baseInput = params.Input.OfInputItemList
	}

	// Track active tool calls
	activeTools := make(map[string]*activeToolCall)

	// Emit AI SDK UI stream start and first step
	oc.emitUIStart(ctx, portal, state, meta)
	oc.emitUIStepStart(ctx, portal, state)

	// Process stream events - no debouncing, stream every delta immediately
	for stream.Next() {
		streamEvent := stream.Current()
		if streamEvent.Type != "error" {
			oc.markMessageSendSuccess(ctx, portal, evt, state)
		}

		switch streamEvent.Type {
		case "response.created", "response.queued", "response.in_progress":
			if strings.TrimSpace(streamEvent.Response.ID) != "" {
				state.responseID = streamEvent.Response.ID
			}
			oc.emitUIRuntimeMetadata(ctx, portal, state, meta, responseMetadataDeltaFromResponse(streamEvent.Response))

		case "response.failed":
			state.finishReason = "error"
			if strings.TrimSpace(streamEvent.Response.ID) != "" {
				state.responseID = streamEvent.Response.ID
			}
			oc.emitUIRuntimeMetadata(ctx, portal, state, meta, responseMetadataDeltaFromResponse(streamEvent.Response))
			if msg := strings.TrimSpace(streamEvent.Response.Error.Message); msg != "" {
				oc.emitUIError(ctx, portal, state, msg)
			}

		case "response.incomplete":
			state.finishReason = strings.TrimSpace(string(streamEvent.Response.IncompleteDetails.Reason))
			if strings.TrimSpace(state.finishReason) == "" {
				state.finishReason = "other"
			}
			if strings.TrimSpace(streamEvent.Response.ID) != "" {
				state.responseID = streamEvent.Response.ID
			}
			oc.emitUIRuntimeMetadata(ctx, portal, state, meta, responseMetadataDeltaFromResponse(streamEvent.Response))

		case "response.output_item.added":
			oc.handleResponseOutputItemAdded(ctx, portal, state, activeTools, streamEvent.Item)

		case "response.output_item.done":
			oc.handleResponseOutputItemDone(ctx, portal, state, activeTools, streamEvent.Item)

		case "response.custom_tool_call_input.delta":
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
				if itemDesc.ok {
					tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
				}
			}
			if tool != nil {
				tool.input.WriteString(streamEvent.Delta)
				oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, streamEvent.Delta, tool.toolType == ToolTypeProvider)
			}

		case "response.custom_tool_call_input.done":
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
				if itemDesc.ok {
					tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
				}
			}
			if tool != nil {
				if tool.input.Len() == 0 && strings.TrimSpace(streamEvent.Input) != "" {
					tool.input.WriteString(streamEvent.Input)
				}
				oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, parseJSONOrRaw(tool.input.String()), tool.toolType == ToolTypeProvider)
			}

		case "response.code_interpreter_call_code.delta":
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
				if itemDesc.ok {
					tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
				}
			}
			if tool != nil {
				tool.input.WriteString(streamEvent.Delta)
				oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, streamEvent.Delta, true)
			}

		case "response.code_interpreter_call_code.done":
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
				if itemDesc.ok {
					tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
				}
			}
			if tool != nil {
				if tool.input.Len() == 0 && strings.TrimSpace(streamEvent.Code) != "" {
					tool.input.WriteString(streamEvent.Code)
				}
				oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, parseJSONOrRaw(tool.input.String()), true)
			}

		case "response.mcp_call_arguments.delta":
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
				if itemDesc.ok {
					tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
				}
			}
			if tool != nil {
				tool.input.WriteString(streamEvent.Delta)
				oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, streamEvent.Delta, true)
			}

		case "response.mcp_call_arguments.done":
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
				if itemDesc.ok {
					tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
				}
			}
			if tool != nil {
				if tool.input.Len() == 0 && strings.TrimSpace(streamEvent.Arguments) != "" {
					tool.input.WriteString(streamEvent.Arguments)
				}
				oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, parseJSONOrRaw(tool.input.String()), true)
			}

		case "response.mcp_call.failed":
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
				if itemDesc.ok {
					tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
				}
			}
			if tool != nil {
				if state != nil && state.uiToolOutputFinalized[tool.callID] {
					break
				}
				errorText := strings.TrimSpace(streamEvent.Item.Error)
				if errorText == "" {
					errorText = "MCP tool call failed"
				}
				denied := outputItemLooksDenied(streamEvent.Item)
				if denied {
					oc.emitUIToolOutputDenied(ctx, portal, state, tool.callID)
				} else {
					oc.emitUIToolOutputError(ctx, portal, state, tool.callID, errorText, true)
				}

				output := map[string]any{}
				if denied {
					output["status"] = "denied"
				} else {
					output["error"] = errorText
				}
				resultPayload := errorText
				if denied && resultPayload == "" {
					resultPayload = "Denied"
				}
				resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, resultPayload, ResultStatusError)
				state.toolCalls = append(state.toolCalls, ToolCallMetadata{
					CallID:        tool.callID,
					ToolName:      tool.toolName,
					ToolType:      string(tool.toolType),
					Output:        output,
					Status:        string(ToolStatusFailed),
					ResultStatus:  string(ResultStatusError),
					ErrorMessage:  errorText,
					StartedAtMs:   tool.startedAtMs,
					CompletedAtMs: time.Now().UnixMilli(),
					CallEventID:   string(tool.eventID),
					ResultEventID: string(resultEventID),
				})
			}

		case "response.output_text.delta":
			touchTyping()
			state.accumulated.WriteString(streamEvent.Delta)
			parsed := (*streamingDirectiveResult)(nil)
			if state.replyAccumulator != nil {
				parsed = state.replyAccumulator.Consume(streamEvent.Delta, false)
			}
			if parsed != nil {
				oc.applyStreamingReplyTarget(state, parsed)
				cleaned := parsed.Text
				if typingSignals != nil {
					typingSignals.SignalTextDelta(cleaned)
				}
				if cleaned != "" {
					state.visibleAccumulated.WriteString(cleaned)
					// First token - send initial message synchronously to capture event_id
					if state.firstToken && state.visibleAccumulated.Len() > 0 {
						state.firstToken = false
						state.firstTokenAtMs = time.Now().UnixMilli()
						if !state.suppressSend && !isHeartbeat {
							// Ensure ghost display name is set before sending the first message
							oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
							state.initialEventID = oc.sendInitialStreamMessage(ctx, portal, state.visibleAccumulated.String(), state.turnID, state.replyTarget)
							if state.initialEventID == "" {
								errText := "failed to send initial streaming message"
								log.Error().Msg("Failed to send initial streaming message")
								state.finishReason = "error"
								oc.emitUIError(ctx, portal, state, errText)
								oc.emitUIFinish(ctx, portal, state, meta)
								return false, nil, &PreDeltaError{Err: errors.New(errText)}
							}
						}
					}
					oc.emitUITextDelta(ctx, portal, state, cleaned)
				}
			}

		case "response.reasoning_text.delta":
			touchTyping()
			if typingSignals != nil {
				typingSignals.SignalReasoningDelta()
			}
			state.reasoning.WriteString(streamEvent.Delta)

			// Check if this is first content (reasoning before text)
			if state.firstToken && state.reasoning.Len() > 0 {
				state.firstToken = false
				state.firstTokenAtMs = time.Now().UnixMilli()
				if !state.suppressSend && !isHeartbeat {
					oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
					// Send empty initial message - will be replaced with content later
					state.initialEventID = oc.sendInitialStreamMessage(ctx, portal, "...", state.turnID, state.replyTarget)
					if state.initialEventID == "" {
						errText := "failed to send initial streaming message"
						log.Error().Msg("Failed to send initial streaming message")
						state.finishReason = "error"
						oc.emitUIError(ctx, portal, state, errText)
						oc.emitUIFinish(ctx, portal, state, meta)
						return false, nil, &PreDeltaError{Err: errors.New(errText)}
					}
				}
			}

			oc.emitUIReasoningDelta(ctx, portal, state, streamEvent.Delta)

		case "response.reasoning_summary_text.delta":
			if strings.TrimSpace(streamEvent.Delta) != "" {
				state.reasoning.WriteString(streamEvent.Delta)
				oc.emitUIReasoningDelta(ctx, portal, state, streamEvent.Delta)
			}

		case "response.reasoning_text.done", "response.reasoning_summary_text.done":
			if strings.TrimSpace(streamEvent.Text) != "" {
				state.reasoning.WriteString(streamEvent.Text)
				oc.emitUIReasoningDelta(ctx, portal, state, streamEvent.Text)
			}

		case "response.refusal.delta":
			touchTyping()
			if typingSignals != nil {
				typingSignals.SignalTextDelta(streamEvent.Delta)
			}
			oc.emitUITextDelta(ctx, portal, state, streamEvent.Delta)

		case "response.refusal.done":
			if strings.TrimSpace(streamEvent.Refusal) != "" {
				oc.emitUITextDelta(ctx, portal, state, streamEvent.Refusal)
			}

		case "response.output_text.done":
			// text-end is emitted from emitUIFinish to keep one contiguous part.

		case "response.function_call_arguments.delta":
			touchTyping()
			if typingSignals != nil {
				typingSignals.SignalToolStart()
			}
			// Get or create active tool call
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				callID := streamEvent.ItemID
				if strings.TrimSpace(callID) == "" {
					callID = NewCallID()
				}
				tool = &activeToolCall{
					callID:      callID,
					toolName:    streamEvent.Name,
					toolType:    ToolTypeFunction,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				activeTools[streamEvent.ItemID] = tool

				if state.initialEventID == "" && !state.suppressSend {
					oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
				}
				if strings.TrimSpace(tool.toolName) != "" {
					tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
				}
			}

			// Accumulate arguments
			tool.input.WriteString(streamEvent.Delta)

			oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, streamEvent.Name, streamEvent.Delta, tool.toolType == ToolTypeProvider)

		case "response.function_call_arguments.done":
			touchTyping()
			if typingSignals != nil {
				typingSignals.SignalToolStart()
			}
			// Function call complete - execute the tool and send result
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				// Create tool if we missed the delta events
				callID := streamEvent.ItemID
				if strings.TrimSpace(callID) == "" {
					callID = NewCallID()
				}
				tool = &activeToolCall{
					callID:      callID,
					toolName:    streamEvent.Name,
					toolType:    ToolTypeFunction,
					startedAtMs: time.Now().UnixMilli(),
				}
				tool.input.WriteString(streamEvent.Arguments)
				activeTools[streamEvent.ItemID] = tool
			}

			// Store the item ID for continuation (this is the call_id for the Responses API)
			tool.itemID = streamEvent.ItemID

			toolName := strings.TrimSpace(tool.toolName)
			if toolName == "" {
				toolName = strings.TrimSpace(streamEvent.Name)
			}
			tool.toolName = toolName
			if tool.eventID == "" {
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			}
			argsJSON := strings.TrimSpace(tool.input.String())
			if argsJSON == "" {
				argsJSON = strings.TrimSpace(streamEvent.Arguments)
			}
			argsJSON = normalizeToolArgsJSON(argsJSON)

			var inputMap any
			if err := json.Unmarshal([]byte(argsJSON), &inputMap); err != nil {
				inputMap = argsJSON
				oc.emitUIToolInputError(ctx, portal, state, tool.callID, toolName, argsJSON, "Invalid JSON tool input", tool.toolType == ToolTypeProvider, false)
			}
			oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, toolName, inputMap, tool.toolType == ToolTypeProvider)

			resultStatus := ResultStatusSuccess
			var result string
			if !oc.isToolEnabled(meta, toolName) {
				resultStatus = ResultStatusError
				result = fmt.Sprintf("Error: tool %s is disabled", toolName)
			} else {
				// Tool approval gating for dangerous builtin tools.
				if argsObj, ok := inputMap.(map[string]any); ok {
					required, action := oc.builtinToolApprovalRequirement(toolName, argsObj)
					if required && oc.isBuiltinAlwaysAllowed(toolName, action) {
						required = false
					}
					if required && state.heartbeat != nil {
						required = false
					}
					if required {
						approvalID := NewCallID()
						ttl := time.Duration(oc.toolApprovalsTTLSeconds()) * time.Second
						oc.registerToolApproval(struct {
							ApprovalID string
							RoomID     id.RoomID
							TurnID     string

							ToolCallID string
							ToolName   string

							ToolKind     ToolApprovalKind
							RuleToolName string
							ServerLabel  string
							Action       string

							TTL time.Duration
						}{
							ApprovalID:   approvalID,
							RoomID:       state.roomID,
							TurnID:       state.turnID,
							ToolCallID:   tool.callID,
							ToolName:     toolName,
							ToolKind:     ToolApprovalKindBuiltin,
							RuleToolName: toolName,
							Action:       action,
							TTL:          ttl,
						})
						oc.emitUIToolApprovalRequest(ctx, portal, state, approvalID, tool.callID, toolName, tool.eventID, oc.toolApprovalsTTLSeconds())
						decision, _, ok := oc.waitToolApproval(ctx, approvalID)
						if !ok {
							if oc.toolApprovalsAskFallback() == "allow" {
								decision = ToolApprovalDecision{Approve: true, Reason: "fallback"}
							} else {
								decision = ToolApprovalDecision{Approve: false, Reason: "timeout"}
							}
						}
						if !decision.Approve {
							resultStatus = ResultStatusDenied
							result = "Denied by user"
							oc.emitUIToolOutputDenied(ctx, portal, state, tool.callID)
						}
					}
				} else {
					// If we couldn't parse args as JSON object, still gate by tool name.
					required, action := oc.builtinToolApprovalRequirement(toolName, nil)
					if required && oc.isBuiltinAlwaysAllowed(toolName, action) {
						required = false
					}
					if required && state.heartbeat != nil {
						required = false
					}
					if required {
						approvalID := NewCallID()
						ttl := time.Duration(oc.toolApprovalsTTLSeconds()) * time.Second
						oc.registerToolApproval(struct {
							ApprovalID   string
							RoomID       id.RoomID
							TurnID       string
							ToolCallID   string
							ToolName     string
							ToolKind     ToolApprovalKind
							RuleToolName string
							ServerLabel  string
							Action       string
							TTL          time.Duration
						}{
							ApprovalID:   approvalID,
							RoomID:       state.roomID,
							TurnID:       state.turnID,
							ToolCallID:   tool.callID,
							ToolName:     toolName,
							ToolKind:     ToolApprovalKindBuiltin,
							RuleToolName: toolName,
							Action:       action,
							TTL:          ttl,
						})
						oc.emitUIToolApprovalRequest(ctx, portal, state, approvalID, tool.callID, toolName, tool.eventID, oc.toolApprovalsTTLSeconds())
						decision, _, ok := oc.waitToolApproval(ctx, approvalID)
						if !ok {
							if oc.toolApprovalsAskFallback() == "allow" {
								decision = ToolApprovalDecision{Approve: true, Reason: "fallback"}
							} else {
								decision = ToolApprovalDecision{Approve: false, Reason: "timeout"}
							}
						}
						if !decision.Approve {
							resultStatus = ResultStatusDenied
							result = "Denied by user"
							oc.emitUIToolOutputDenied(ctx, portal, state, tool.callID)
						}
					}
				}

				// If denied, skip tool execution but still send a tool result to the model.
				if resultStatus != ResultStatusDenied {
					// Wrap context with bridge info for tools that need it (e.g., channel-edit, react)
					toolCtx := WithBridgeToolContext(ctx, &BridgeToolContext{
						Client:        oc,
						Portal:        portal,
						Meta:          meta,
						SourceEventID: state.sourceEventID,
						SenderID:      state.senderID,
					})
					var err error
					result, err = oc.executeBuiltinTool(toolCtx, portal, toolName, argsJSON)
					if err != nil {
						log.Warn().Err(err).Str("tool", toolName).Msg("Tool execution failed")
						result = fmt.Sprintf("Error: %s", err.Error())
						resultStatus = ResultStatusError
					}
				}
			}

			// Check for TTS audio result (AUDIO: prefix)
			displayResult := result
			if strings.HasPrefix(result, TTSResultPrefix) {
				audioB64 := strings.TrimPrefix(result, TTSResultPrefix)
				audioData, err := base64.StdEncoding.DecodeString(audioB64)
				if err != nil {
					log.Warn().Err(err).Msg("Failed to decode TTS audio")
					displayResult = "Error: failed to decode TTS audio"
					resultStatus = ResultStatusError
				} else {
					mimeType := detectAudioMime(audioData, "audio/mpeg")
					// Send audio message
					if _, mediaURL, err := oc.sendGeneratedAudio(ctx, portal, audioData, mimeType, state.turnID); err != nil {
						log.Warn().Err(err).Msg("Failed to send TTS audio")
						displayResult = "Error: failed to send TTS audio"
						resultStatus = ResultStatusError
					} else {
						recordGeneratedFile(state, mediaURL, mimeType)
						oc.emitUIFile(ctx, portal, state, mediaURL, mimeType)
						displayResult = "Audio message sent successfully"
					}
				}
				result = displayResult
			}

			// Check for image generation result (IMAGE: / IMAGES: prefix)
			if strings.HasPrefix(result, ImagesResultPrefix) {
				payload := strings.TrimPrefix(result, ImagesResultPrefix)
				var images []string
				if err := json.Unmarshal([]byte(payload), &images); err != nil {
					log.Warn().Err(err).Msg("Failed to parse generated images payload")
					displayResult = "Error: failed to parse generated images"
					resultStatus = ResultStatusError
				} else {
					success := 0
					for _, imageB64 := range images {
						imageData, mimeType, err := decodeBase64Image(imageB64)
						if err != nil {
							log.Warn().Err(err).Msg("Failed to decode generated image")
							continue
						}
						_, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turnID)
						if err != nil {
							log.Warn().Err(err).Msg("Failed to send generated image")
							continue
						}
						recordGeneratedFile(state, mediaURL, mimeType)
						oc.emitUIFile(ctx, portal, state, mediaURL, mimeType)
						success++
					}
					if success == len(images) && success > 0 {
						displayResult = fmt.Sprintf("Images generated and sent successfully (%d)", success)
					} else if success > 0 {
						displayResult = fmt.Sprintf("Images generated with %d/%d sent successfully", success, len(images))
						resultStatus = ResultStatusError
					} else {
						displayResult = "Error: failed to send generated images"
						resultStatus = ResultStatusError
					}
				}
				result = displayResult
			} else if strings.HasPrefix(result, ImageResultPrefix) {
				imageB64 := strings.TrimPrefix(result, ImageResultPrefix)
				imageData, mimeType, err := decodeBase64Image(imageB64)
				if err != nil {
					log.Warn().Err(err).Msg("Failed to decode generated image")
					displayResult = "Error: failed to decode generated image"
					resultStatus = ResultStatusError
				} else {
					// Send image message
					if _, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turnID); err != nil {
						log.Warn().Err(err).Msg("Failed to send generated image")
						displayResult = "Error: failed to send generated image"
						resultStatus = ResultStatusError
					} else {
						recordGeneratedFile(state, mediaURL, mimeType)
						oc.emitUIFile(ctx, portal, state, mediaURL, mimeType)
						displayResult = "Image generated and sent successfully"
					}
				}
				result = displayResult
			}

			// Store result for API continuation
			tool.result = result
			collectToolOutputCitations(state, toolName, result)
			args := argsJSON
			state.pendingFunctionOutputs = append(state.pendingFunctionOutputs, functionCallOutput{
				callID:    streamEvent.ItemID,
				name:      toolName,
				arguments: args,
				output:    result,
			})

			// Emit UI tool output immediately so the desktop sees the tool
			// as completed without waiting for the timeline event send.
			if resultStatus == ResultStatusSuccess {
				oc.emitUIToolOutputAvailable(ctx, portal, state, tool.callID, result, tool.toolType == ToolTypeProvider, false)
			} else if resultStatus != ResultStatusDenied {
				oc.emitUIToolOutputError(ctx, portal, state, tool.callID, result, tool.toolType == ToolTypeProvider)
			}

			// Normalize input for storage
			inputMapForMeta := map[string]any{}
			if parsed, ok := inputMap.(map[string]any); ok {
				inputMapForMeta = parsed
			} else if raw, ok := inputMap.(string); ok && raw != "" {
				inputMapForMeta = map[string]any{"_raw": raw}
			}

			// Track tool call in metadata (sendToolResultEvent is a blocking
			// Matrix API call, but the UI update was already emitted above).
			completedAt := time.Now().UnixMilli()
			resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, result, resultStatus)
			state.toolCalls = append(state.toolCalls, ToolCallMetadata{
				CallID:        tool.callID,
				ToolName:      toolName,
				ToolType:      string(tool.toolType),
				Input:         inputMapForMeta,
				Output:        map[string]any{"result": result},
				Status:        string(ToolStatusCompleted),
				ResultStatus:  string(resultStatus),
				StartedAtMs:   tool.startedAtMs,
				CompletedAtMs: completedAt,
				CallEventID:   string(tool.eventID),
				ResultEventID: string(resultEventID),
			})

		case "response.file_search_call.searching", "response.file_search_call.in_progress":
			callID := strings.TrimSpace(streamEvent.ItemID)
			if callID == "" {
				callID = NewCallID()
			}
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				tool = &activeToolCall{
					callID:      callID,
					toolName:    "file_search",
					toolType:    ToolTypeProvider,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				activeTools[streamEvent.ItemID] = tool
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			}
			oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, "", true)

		case "response.file_search_call.completed":
			tool, exists := activeTools[streamEvent.ItemID]
			callID := strings.TrimSpace(streamEvent.ItemID)
			if callID == "" {
				callID = NewCallID()
			}
			if exists && tool != nil {
				callID = tool.callID
			}
			if state != nil && state.uiToolOutputFinalized[callID] {
				break
			}
			if !exists {
				tool = &activeToolCall{
					callID:      callID,
					toolName:    "file_search",
					toolType:    ToolTypeProvider,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				activeTools[streamEvent.ItemID] = tool
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			}
			output := map[string]any{"status": "completed"}
			oc.emitUIToolOutputAvailable(ctx, portal, state, callID, output, true, false)

			resultJSON, _ := json.Marshal(output)
			resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, string(resultJSON), ResultStatusSuccess)
			state.toolCalls = append(state.toolCalls, ToolCallMetadata{
				CallID:        callID,
				ToolName:      "file_search",
				ToolType:      string(tool.toolType),
				Output:        output,
				Status:        string(ToolStatusCompleted),
				ResultStatus:  string(ResultStatusSuccess),
				StartedAtMs:   tool.startedAtMs,
				CompletedAtMs: time.Now().UnixMilli(),
				CallEventID:   string(tool.eventID),
				ResultEventID: string(resultEventID),
			})

		case "response.code_interpreter_call.in_progress", "response.code_interpreter_call.interpreting":
			callID := strings.TrimSpace(streamEvent.ItemID)
			if callID == "" {
				callID = NewCallID()
			}
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				tool = &activeToolCall{
					callID:      callID,
					toolName:    "code_interpreter",
					toolType:    ToolTypeProvider,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				activeTools[streamEvent.ItemID] = tool
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			}
			oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, "", true)

		case "response.code_interpreter_call.completed":
			tool, exists := activeTools[streamEvent.ItemID]
			callID := strings.TrimSpace(streamEvent.ItemID)
			if callID == "" {
				callID = NewCallID()
			}
			if exists && tool != nil {
				callID = tool.callID
			}
			if state != nil && state.uiToolOutputFinalized[callID] {
				break
			}
			if !exists {
				tool = &activeToolCall{
					callID:      callID,
					toolName:    "code_interpreter",
					toolType:    ToolTypeProvider,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				activeTools[streamEvent.ItemID] = tool
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			}
			output := map[string]any{"status": "completed"}
			oc.emitUIToolOutputAvailable(ctx, portal, state, callID, output, true, false)

			resultJSON, _ := json.Marshal(output)
			resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, string(resultJSON), ResultStatusSuccess)
			state.toolCalls = append(state.toolCalls, ToolCallMetadata{
				CallID:        callID,
				ToolName:      "code_interpreter",
				ToolType:      string(tool.toolType),
				Output:        output,
				Status:        string(ToolStatusCompleted),
				ResultStatus:  string(ResultStatusSuccess),
				StartedAtMs:   tool.startedAtMs,
				CompletedAtMs: time.Now().UnixMilli(),
				CallEventID:   string(tool.eventID),
				ResultEventID: string(resultEventID),
			})

		case "response.mcp_list_tools.in_progress":
			callID := strings.TrimSpace(streamEvent.ItemID)
			if callID == "" {
				callID = NewCallID()
			}
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				tool = &activeToolCall{
					callID:      callID,
					toolName:    "mcp.list_tools",
					toolType:    ToolTypeMCP,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				activeTools[streamEvent.ItemID] = tool
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			}
			oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, "", true)

		case "response.mcp_list_tools.completed":
			tool, exists := activeTools[streamEvent.ItemID]
			callID := strings.TrimSpace(streamEvent.ItemID)
			if callID == "" {
				callID = NewCallID()
			}
			if exists && tool != nil {
				callID = tool.callID
			}
			if state != nil && state.uiToolOutputFinalized[callID] {
				break
			}
			if !exists {
				tool = &activeToolCall{
					callID:      callID,
					toolName:    "mcp.list_tools",
					toolType:    ToolTypeMCP,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				activeTools[streamEvent.ItemID] = tool
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			}
			output := map[string]any{"status": "completed"}
			oc.emitUIToolOutputAvailable(ctx, portal, state, callID, output, true, false)

			resultJSON, _ := json.Marshal(output)
			resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, string(resultJSON), ResultStatusSuccess)
			state.toolCalls = append(state.toolCalls, ToolCallMetadata{
				CallID:        callID,
				ToolName:      "mcp.list_tools",
				ToolType:      string(tool.toolType),
				Output:        output,
				Status:        string(ToolStatusCompleted),
				ResultStatus:  string(ResultStatusSuccess),
				StartedAtMs:   tool.startedAtMs,
				CompletedAtMs: time.Now().UnixMilli(),
				CallEventID:   string(tool.eventID),
				ResultEventID: string(resultEventID),
			})

		case "response.mcp_list_tools.failed":
			tool, exists := activeTools[streamEvent.ItemID]
			callID := strings.TrimSpace(streamEvent.ItemID)
			if callID == "" {
				callID = NewCallID()
			}
			if exists && tool != nil {
				callID = tool.callID
			}
			if state != nil && state.uiToolOutputFinalized[callID] {
				break
			}
			if !exists {
				tool = &activeToolCall{
					callID:      callID,
					toolName:    "mcp.list_tools",
					toolType:    ToolTypeMCP,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				activeTools[streamEvent.ItemID] = tool
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			}
			errText := "MCP list tools failed"
			oc.emitUIToolOutputError(ctx, portal, state, callID, errText, true)

			resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, errText, ResultStatusError)
			state.toolCalls = append(state.toolCalls, ToolCallMetadata{
				CallID:        callID,
				ToolName:      "mcp.list_tools",
				ToolType:      string(tool.toolType),
				Output:        map[string]any{"error": errText},
				Status:        string(ToolStatusFailed),
				ResultStatus:  string(ResultStatusError),
				ErrorMessage:  errText,
				StartedAtMs:   tool.startedAtMs,
				CompletedAtMs: time.Now().UnixMilli(),
				CallEventID:   string(tool.eventID),
				ResultEventID: string(resultEventID),
			})

		case "response.mcp_call.in_progress":
			callID := strings.TrimSpace(streamEvent.ItemID)
			if callID == "" {
				callID = NewCallID()
			}
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				tool = &activeToolCall{
					callID:      callID,
					toolName:    "mcp.call",
					toolType:    ToolTypeMCP,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				activeTools[streamEvent.ItemID] = tool
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			}
			oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, "", true)

		case "response.mcp_call.completed":
			tool, exists := activeTools[streamEvent.ItemID]
			callID := strings.TrimSpace(streamEvent.ItemID)
			if callID == "" {
				callID = NewCallID()
			}
			if exists && tool != nil {
				callID = tool.callID
			}
			if state != nil && state.uiToolOutputFinalized[callID] {
				break
			}
			if !exists {
				tool = &activeToolCall{
					callID:      callID,
					toolName:    "mcp.call",
					toolType:    ToolTypeMCP,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				activeTools[streamEvent.ItemID] = tool
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			}
			output := map[string]any{"status": "completed"}
			oc.emitUIToolOutputAvailable(ctx, portal, state, callID, output, true, false)

			resultJSON, _ := json.Marshal(output)
			resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, string(resultJSON), ResultStatusSuccess)
			state.toolCalls = append(state.toolCalls, ToolCallMetadata{
				CallID:        callID,
				ToolName:      "mcp.call",
				ToolType:      string(tool.toolType),
				Output:        output,
				Status:        string(ToolStatusCompleted),
				ResultStatus:  string(ResultStatusSuccess),
				StartedAtMs:   tool.startedAtMs,
				CompletedAtMs: time.Now().UnixMilli(),
				CallEventID:   string(tool.eventID),
				ResultEventID: string(resultEventID),
			})

		case "response.web_search_call.searching", "response.web_search_call.in_progress":
			touchTyping()
			if typingSignals != nil {
				typingSignals.SignalToolStart()
			}
			// Web search starting
			callID := streamEvent.ItemID
			if strings.TrimSpace(callID) == "" {
				callID = NewCallID()
			}
			tool := &activeToolCall{
				callID:      callID,
				toolName:    "web_search",
				toolType:    ToolTypeProvider,
				startedAtMs: time.Now().UnixMilli(),
				itemID:      streamEvent.ItemID,
			}
			tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
			activeTools[streamEvent.ItemID] = tool

			if state.initialEventID == "" && !state.suppressSend {
				oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
			}
			oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, "web_search", "", true)

		case "response.web_search_call.completed":
			touchTyping()
			// Web search completed
			tool, exists := activeTools[streamEvent.ItemID]
			callID := ""
			if exists && tool != nil {
				callID = tool.callID
			}
			if callID == "" {
				callID = streamEvent.ItemID
			}
			if exists {
				// Track tool call
				output := map[string]any{"status": "completed"}
				resultJSON, _ := json.Marshal(output)
				resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, string(resultJSON), ResultStatusSuccess)
				state.toolCalls = append(state.toolCalls, ToolCallMetadata{
					CallID:        callID,
					ToolName:      "web_search",
					ToolType:      string(tool.toolType),
					Output:        output,
					Status:        string(ToolStatusCompleted),
					ResultStatus:  string(ResultStatusSuccess),
					StartedAtMs:   tool.startedAtMs,
					CompletedAtMs: time.Now().UnixMilli(),
					CallEventID:   string(tool.eventID),
					ResultEventID: string(resultEventID),
				})
			}
			oc.emitUIToolOutputAvailable(ctx, portal, state, callID, map[string]any{"status": "completed"}, true, false)

		case "response.image_generation_call.in_progress", "response.image_generation_call.generating":
			touchTyping()
			if typingSignals != nil {
				typingSignals.SignalToolStart()
			}
			// Image generation in progress
			tool, exists := activeTools[streamEvent.ItemID]
			if !exists {
				callID := streamEvent.ItemID
				if strings.TrimSpace(callID) == "" {
					callID = NewCallID()
				}
				tool = &activeToolCall{
					callID:      callID,
					toolName:    "image_generation",
					toolType:    ToolTypeProvider,
					startedAtMs: time.Now().UnixMilli(),
					itemID:      streamEvent.ItemID,
				}
				tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
				activeTools[streamEvent.ItemID] = tool

				if state.initialEventID == "" && !state.suppressSend {
					oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
				}
			}
			oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, "image_generation", "", true)

			log.Debug().Str("item_id", streamEvent.ItemID).Msg("Image generation in progress")

		case "response.image_generation_call.completed":
			touchTyping()
			if typingSignals != nil {
				typingSignals.SignalToolStart()
			}
			// Image generation completed - the actual image data will be in response.completed
			tool, exists := activeTools[streamEvent.ItemID]
			callID := ""
			if exists && tool != nil {
				callID = tool.callID
			}
			if callID == "" {
				callID = streamEvent.ItemID
			}
			if exists {
				// Track tool call
				output := map[string]any{"status": "completed"}
				resultJSON, _ := json.Marshal(output)
				resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, string(resultJSON), ResultStatusSuccess)
				state.toolCalls = append(state.toolCalls, ToolCallMetadata{
					CallID:        callID,
					ToolName:      "image_generation",
					ToolType:      string(tool.toolType),
					Output:        output,
					Status:        string(ToolStatusCompleted),
					ResultStatus:  string(ResultStatusSuccess),
					StartedAtMs:   tool.startedAtMs,
					CompletedAtMs: time.Now().UnixMilli(),
					CallEventID:   string(tool.eventID),
					ResultEventID: string(resultEventID),
				})
			}
			log.Info().Str("item_id", streamEvent.ItemID).Msg("Image generation completed")
			oc.emitUIToolOutputAvailable(ctx, portal, state, callID, map[string]any{"status": "completed"}, true, false)

		case "response.image_generation_call.partial_image":
			touchTyping()
			if typingSignals != nil {
				typingSignals.SignalToolStart()
			}
			oc.emitStreamEvent(ctx, portal, state, map[string]any{
				"type":      "data-image_generation_partial",
				"data":      map[string]any{"item_id": streamEvent.ItemID, "index": streamEvent.PartialImageIndex, "image_b64": streamEvent.PartialImageB64},
				"transient": true,
			})

		case "response.output_text.annotation.added":
			if citation, ok := extractURLCitation(streamEvent.Annotation); ok {
				state.sourceCitations = mergeSourceCitations(state.sourceCitations, []sourceCitation{citation})
				oc.emitUISourceURL(ctx, portal, state, citation)
			}
			if document, ok := extractDocumentCitation(streamEvent.Annotation); ok {
				state.sourceDocuments = append(state.sourceDocuments, document)
				oc.emitUISourceDocument(ctx, portal, state, document)
			}
			oc.emitStreamEvent(ctx, portal, state, map[string]any{
				"type":      "data-annotation",
				"data":      map[string]any{"annotation": streamEvent.Annotation, "index": streamEvent.AnnotationIndex},
				"transient": true,
			})

		case "response.completed":
			state.completedAtMs = time.Now().UnixMilli()

			if streamEvent.Response.Usage.TotalTokens > 0 || streamEvent.Response.Usage.InputTokens > 0 || streamEvent.Response.Usage.OutputTokens > 0 {
				state.promptTokens = streamEvent.Response.Usage.InputTokens
				state.completionTokens = streamEvent.Response.Usage.OutputTokens
				state.reasoningTokens = streamEvent.Response.Usage.OutputTokensDetails.ReasoningTokens
				state.totalTokens = streamEvent.Response.Usage.TotalTokens
			}

			if streamEvent.Response.Status == "completed" {
				state.finishReason = "stop"
			} else {
				state.finishReason = string(streamEvent.Response.Status)
			}
			// Capture response ID for persistence (will save to DB and portal after streaming completes)
			if streamEvent.Response.ID != "" {
				state.responseID = streamEvent.Response.ID
			}
			oc.emitUIMessageMetadata(ctx, portal, state, oc.buildUIMessageMetadata(state, meta, true))

			// Extract any generated images from response output
			for _, output := range streamEvent.Response.Output {
				if output.Type == "image_generation_call" {
					imgOutput := output.AsImageGenerationCall()
					if imgOutput.Status == "completed" && imgOutput.Result != "" {
						state.pendingImages = append(state.pendingImages, generatedImage{
							itemID:   imgOutput.ID,
							imageB64: imgOutput.Result,
							turnID:   state.turnID,
						})
						log.Debug().Str("item_id", imgOutput.ID).Msg("Captured generated image from response")
					}
				}
			}

			log.Debug().Str("reason", state.finishReason).Str("response_id", state.responseID).Int("images", len(state.pendingImages)).Msg("Response stream completed")

		case "error":
			apiErr := fmt.Errorf("API error: %s", streamEvent.Message)
			state.finishReason = "error"
			oc.emitUIError(ctx, portal, state, streamEvent.Message)
			oc.emitUIFinish(ctx, portal, state, meta)
			logResponsesFailure(log, apiErr, params, meta, messages, "stream_event_error")
			// Check for context length error
			if strings.Contains(streamEvent.Message, "context_length") || strings.Contains(streamEvent.Message, "token") {
				return false, &ContextLengthError{
					OriginalError: fmt.Errorf("%s", streamEvent.Message),
				}, nil
			}
			if state.initialEventID != "" {
				return false, nil, &NonFallbackError{Err: apiErr}
			}
			return false, nil, &PreDeltaError{Err: apiErr}
		default:
			// Ignore unknown events
		}
	}

	oc.emitUIStepFinish(ctx, portal, state)

	// Check for stream errors
	if err := stream.Err(); err != nil {
		logResponsesFailure(log, err, params, meta, messages, "stream_err")
		if errors.Is(err, context.Canceled) {
			state.finishReason = "cancelled"
			// Flush partial content if we already sent some deltas
			if state.initialEventID != "" && state.accumulated.Len() > 0 {
				oc.flushPartialStreamingMessage(context.Background(), portal, state, meta)
			}
			oc.emitUIAbort(ctx, portal, state, "cancelled")
			oc.emitUIFinish(ctx, portal, state, meta)
			if state.initialEventID != "" {
				return false, nil, &NonFallbackError{Err: err}
			}
			return false, nil, &PreDeltaError{Err: err}
		}
		cle := ParseContextLengthError(err)
		if cle != nil {
			return false, cle, nil
		}
		state.finishReason = "error"
		oc.emitUIError(ctx, portal, state, err.Error())
		oc.emitUIFinish(ctx, portal, state, meta)
		if state.initialEventID != "" {
			return false, nil, &NonFallbackError{Err: err}
		}
		return false, nil, &PreDeltaError{Err: err}
	}

	// If there are pending tool outputs or MCP approvals, send them back to the API for continuation.
	// This loop continues until the model generates a response without additional tool actions.
	continuationRound := 0
	for (len(state.pendingFunctionOutputs) > 0 || len(state.pendingMcpApprovals) > 0) && state.responseID != "" {
		// Check for context cancellation before starting a new continuation round
		if ctx.Err() != nil {
			state.finishReason = "cancelled"
			if state.initialEventID != "" && state.accumulated.Len() > 0 {
				oc.flushPartialStreamingMessage(context.Background(), portal, state, meta)
			}
			oc.emitUIAbort(ctx, portal, state, "cancelled")
			oc.emitUIFinish(ctx, portal, state, meta)
			if state.initialEventID != "" {
				return false, nil, &NonFallbackError{Err: ctx.Err()}
			}
			return false, nil, &PreDeltaError{Err: ctx.Err()}
		}

		continuationRound++
		if continuationRound > maxToolRounds {
			err := fmt.Errorf("max responses tool call rounds reached (%d)", maxToolRounds)
			log.Warn().Err(err).Int("pending_outputs", len(state.pendingFunctionOutputs)).Msg("Stopping responses continuation loop")
			state.finishReason = "error"
			oc.emitUIError(ctx, portal, state, err.Error())
			oc.emitUIFinish(ctx, portal, state, meta)
			if state.initialEventID != "" {
				return false, nil, &NonFallbackError{Err: err}
			}
			return false, nil, &PreDeltaError{Err: err}
		}
		log.Debug().
			Int("pending_outputs", len(state.pendingFunctionOutputs)).
			Int("pending_approvals", len(state.pendingMcpApprovals)).
			Str("previous_response_id", state.responseID).
			Msg("Continuing response with pending tool actions")

		pendingOutputs := append([]functionCallOutput(nil), state.pendingFunctionOutputs...)
		pendingApprovals := append([]mcpApprovalRequest(nil), state.pendingMcpApprovals...)

		approvalInputs := make([]responses.ResponseInputItemUnionParam, 0, len(pendingApprovals))
		for _, approval := range pendingApprovals {
			decision, _, ok := oc.waitToolApproval(ctx, approval.approvalID)
			if !ok {
				if oc.toolApprovalsAskFallback() == "allow" {
					decision = ToolApprovalDecision{Approve: true, Reason: "fallback"}
				} else {
					decision = ToolApprovalDecision{Approve: false, Reason: "timeout"}
				}
			}
			item := responses.ResponseInputItemParamOfMcpApprovalResponse(approval.approvalID, decision.Approve)
			if decision.Reason != "" && item.OfMcpApprovalResponse != nil {
				item.OfMcpApprovalResponse.Reason = param.NewOpt(decision.Reason)
			}
			approvalInputs = append(approvalInputs, item)

			if !decision.Approve {
				// Optimistically mark as denied in the UI; the provider may emit a denial later as well.
				oc.emitUIToolOutputDenied(ctx, portal, state, approval.toolCallID)
			}
		}

		// Build continuation request with tool outputs + approval responses
		continuationParams := oc.buildContinuationParams(ctx, state, meta, pendingOutputs, approvalInputs)

		// OpenRouter Responses API is stateless; persist tool calls in base input.
		if oc.isOpenRouterProvider() && len(state.baseInput) > 0 {
			for _, output := range pendingOutputs {
				if output.name != "" {
					args := output.arguments
					if strings.TrimSpace(args) == "" {
						args = "{}"
					}
					state.baseInput = append(state.baseInput, responses.ResponseInputItemParamOfFunctionCall(args, output.callID, output.name))
				}
				state.baseInput = append(state.baseInput, buildFunctionCallOutputItem(output.callID, output.output, true))
			}
			for _, approval := range approvalInputs {
				state.baseInput = append(state.baseInput, approval)
			}
		}

		// Reset active tools for new iteration
		activeTools = make(map[string]*activeToolCall)

		// Start continuation stream
		stream = oc.api.Responses.NewStreaming(ctx, continuationParams)
		if stream == nil {
			initErr := errors.New("continuation streaming not available")
			logResponsesFailure(log, initErr, continuationParams, meta, messages, "continuation_init")
			state.finishReason = "error"
			oc.emitUIError(ctx, portal, state, initErr.Error())
			oc.emitUIFinish(ctx, portal, state, meta)
			if state.initialEventID != "" {
				return false, nil, &NonFallbackError{Err: initErr}
			}
			return false, nil, &PreDeltaError{Err: initErr}
		}
		// Clear pending inputs only once continuation stream has actually started.
		state.pendingFunctionOutputs = nil
		state.pendingMcpApprovals = nil
		oc.emitUIStepStart(ctx, portal, state)

		// Process continuation stream events
		for stream.Next() {
			streamEvent := stream.Current()

			switch streamEvent.Type {
			case "response.created", "response.queued", "response.in_progress":
				if strings.TrimSpace(streamEvent.Response.ID) != "" {
					state.responseID = streamEvent.Response.ID
				}
				oc.emitUIRuntimeMetadata(ctx, portal, state, meta, responseMetadataDeltaFromResponse(streamEvent.Response))

			case "response.failed":
				state.finishReason = "error"
				if strings.TrimSpace(streamEvent.Response.ID) != "" {
					state.responseID = streamEvent.Response.ID
				}
				oc.emitUIRuntimeMetadata(ctx, portal, state, meta, responseMetadataDeltaFromResponse(streamEvent.Response))
				if msg := strings.TrimSpace(streamEvent.Response.Error.Message); msg != "" {
					oc.emitUIError(ctx, portal, state, msg)
				}

			case "response.incomplete":
				state.finishReason = strings.TrimSpace(string(streamEvent.Response.IncompleteDetails.Reason))
				if strings.TrimSpace(state.finishReason) == "" {
					state.finishReason = "other"
				}
				if strings.TrimSpace(streamEvent.Response.ID) != "" {
					state.responseID = streamEvent.Response.ID
				}
				oc.emitUIRuntimeMetadata(ctx, portal, state, meta, responseMetadataDeltaFromResponse(streamEvent.Response))

			case "response.output_item.added":
				oc.handleResponseOutputItemAdded(ctx, portal, state, activeTools, streamEvent.Item)

			case "response.output_item.done":
				oc.handleResponseOutputItemDone(ctx, portal, state, activeTools, streamEvent.Item)

			case "response.custom_tool_call_input.delta":
				tool, exists := activeTools[streamEvent.ItemID]
				if !exists {
					itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
					if itemDesc.ok {
						tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
					}
				}
				if tool != nil {
					tool.input.WriteString(streamEvent.Delta)
					oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, streamEvent.Delta, tool.toolType == ToolTypeProvider)
				}

			case "response.custom_tool_call_input.done":
				tool, exists := activeTools[streamEvent.ItemID]
				if !exists {
					itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
					if itemDesc.ok {
						tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
					}
				}
				if tool != nil {
					if tool.input.Len() == 0 && strings.TrimSpace(streamEvent.Input) != "" {
						tool.input.WriteString(streamEvent.Input)
					}
					oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, parseJSONOrRaw(tool.input.String()), tool.toolType == ToolTypeProvider)
				}

			case "response.code_interpreter_call_code.delta":
				tool, exists := activeTools[streamEvent.ItemID]
				if !exists {
					itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
					if itemDesc.ok {
						tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
					}
				}
				if tool != nil {
					tool.input.WriteString(streamEvent.Delta)
					oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, streamEvent.Delta, true)
				}

			case "response.code_interpreter_call_code.done":
				tool, exists := activeTools[streamEvent.ItemID]
				if !exists {
					itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
					if itemDesc.ok {
						tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
					}
				}
				if tool != nil {
					if tool.input.Len() == 0 && strings.TrimSpace(streamEvent.Code) != "" {
						tool.input.WriteString(streamEvent.Code)
					}
					oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, parseJSONOrRaw(tool.input.String()), true)
				}

			case "response.mcp_call_arguments.delta":
				tool, exists := activeTools[streamEvent.ItemID]
				if !exists {
					itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
					if itemDesc.ok {
						tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
					}
				}
				if tool != nil {
					tool.input.WriteString(streamEvent.Delta)
					oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, streamEvent.Delta, true)
				}

			case "response.mcp_call_arguments.done":
				tool, exists := activeTools[streamEvent.ItemID]
				if !exists {
					itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
					if itemDesc.ok {
						tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
					}
				}
				if tool != nil {
					if tool.input.Len() == 0 && strings.TrimSpace(streamEvent.Arguments) != "" {
						tool.input.WriteString(streamEvent.Arguments)
					}
					oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, tool.toolName, parseJSONOrRaw(tool.input.String()), true)
				}

			case "response.mcp_call.failed":
				tool, exists := activeTools[streamEvent.ItemID]
				if !exists {
					itemDesc := deriveToolDescriptorForOutputItem(streamEvent.Item, state)
					if itemDesc.ok {
						tool = oc.upsertActiveToolFromDescriptor(ctx, portal, state, activeTools, itemDesc)
					}
				}
				if tool != nil {
					if state != nil && state.uiToolOutputFinalized[tool.callID] {
						break
					}
					errorText := strings.TrimSpace(streamEvent.Item.Error)
					if errorText == "" {
						errorText = "MCP tool call failed"
					}
					denied := outputItemLooksDenied(streamEvent.Item)
					if denied {
						oc.emitUIToolOutputDenied(ctx, portal, state, tool.callID)
					} else {
						oc.emitUIToolOutputError(ctx, portal, state, tool.callID, errorText, true)
					}

					output := map[string]any{}
					if denied {
						output["status"] = "denied"
					} else {
						output["error"] = errorText
					}
					resultPayload := errorText
					if denied && resultPayload == "" {
						resultPayload = "Denied"
					}
					resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, resultPayload, ResultStatusError)
					state.toolCalls = append(state.toolCalls, ToolCallMetadata{
						CallID:        tool.callID,
						ToolName:      tool.toolName,
						ToolType:      string(tool.toolType),
						Output:        output,
						Status:        string(ToolStatusFailed),
						ResultStatus:  string(ResultStatusError),
						ErrorMessage:  errorText,
						StartedAtMs:   tool.startedAtMs,
						CompletedAtMs: time.Now().UnixMilli(),
						CallEventID:   string(tool.eventID),
						ResultEventID: string(resultEventID),
					})
				}

			case "response.output_text.delta":
				touchTyping()
				state.accumulated.WriteString(streamEvent.Delta)
				parsed := (*streamingDirectiveResult)(nil)
				if state.replyAccumulator != nil {
					parsed = state.replyAccumulator.Consume(streamEvent.Delta, false)
				}
				if parsed != nil {
					oc.applyStreamingReplyTarget(state, parsed)
					cleaned := parsed.Text
					if typingSignals != nil {
						typingSignals.SignalTextDelta(cleaned)
					}
					if cleaned != "" {
						state.visibleAccumulated.WriteString(cleaned)
						if state.firstToken && state.visibleAccumulated.Len() > 0 {
							state.firstToken = false
							state.firstTokenAtMs = time.Now().UnixMilli()
							if !state.suppressSend && !isHeartbeat {
								oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
								state.initialEventID = oc.sendInitialStreamMessage(ctx, portal, state.visibleAccumulated.String(), state.turnID, state.replyTarget)
								if state.initialEventID == "" {
									errText := "failed to send initial streaming message (continuation)"
									log.Error().Msg("Failed to send initial streaming message (continuation)")
									state.finishReason = "error"
									oc.emitUIError(ctx, portal, state, errText)
									oc.emitUIFinish(ctx, portal, state, meta)
									return false, nil, &PreDeltaError{Err: errors.New(errText)}
								}
							}
						}
						oc.emitUITextDelta(ctx, portal, state, cleaned)
					}
				}

			case "response.reasoning_text.delta":
				touchTyping()
				if typingSignals != nil {
					typingSignals.SignalReasoningDelta()
				}
				state.reasoning.WriteString(streamEvent.Delta)
				if state.firstToken && state.reasoning.Len() > 0 {
					state.firstToken = false
					state.firstTokenAtMs = time.Now().UnixMilli()
					if !state.suppressSend && !isHeartbeat {
						oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
						state.initialEventID = oc.sendInitialStreamMessage(ctx, portal, "...", state.turnID, state.replyTarget)
						if state.initialEventID == "" {
							errText := "failed to send initial streaming message (continuation)"
							log.Error().Msg("Failed to send initial streaming message (continuation)")
							state.finishReason = "error"
							oc.emitUIError(ctx, portal, state, errText)
							oc.emitUIFinish(ctx, portal, state, meta)
							return false, nil, &PreDeltaError{Err: errors.New(errText)}
						}
					}
				}
				oc.emitUIReasoningDelta(ctx, portal, state, streamEvent.Delta)

			case "response.reasoning_summary_text.delta":
				if strings.TrimSpace(streamEvent.Delta) != "" {
					state.reasoning.WriteString(streamEvent.Delta)
					oc.emitUIReasoningDelta(ctx, portal, state, streamEvent.Delta)
				}

			case "response.reasoning_text.done", "response.reasoning_summary_text.done":
				if strings.TrimSpace(streamEvent.Text) != "" {
					state.reasoning.WriteString(streamEvent.Text)
					oc.emitUIReasoningDelta(ctx, portal, state, streamEvent.Text)
				}

			case "response.refusal.delta":
				touchTyping()
				if typingSignals != nil {
					typingSignals.SignalTextDelta(streamEvent.Delta)
				}
				oc.emitUITextDelta(ctx, portal, state, streamEvent.Delta)

			case "response.refusal.done":
				if strings.TrimSpace(streamEvent.Refusal) != "" {
					oc.emitUITextDelta(ctx, portal, state, streamEvent.Refusal)
				}

			case "response.output_text.done":
				// text-end is emitted from emitUIFinish to keep one contiguous part.

			case "response.output_text.annotation.added":
				if citation, ok := extractURLCitation(streamEvent.Annotation); ok {
					state.sourceCitations = mergeSourceCitations(state.sourceCitations, []sourceCitation{citation})
					oc.emitUISourceURL(ctx, portal, state, citation)
				}
				if document, ok := extractDocumentCitation(streamEvent.Annotation); ok {
					state.sourceDocuments = append(state.sourceDocuments, document)
					oc.emitUISourceDocument(ctx, portal, state, document)
				}
				oc.emitStreamEvent(ctx, portal, state, map[string]any{
					"type":      "data-annotation",
					"data":      map[string]any{"annotation": streamEvent.Annotation, "index": streamEvent.AnnotationIndex},
					"transient": true,
				})

			case "response.function_call_arguments.delta":
				touchTyping()
				if typingSignals != nil {
					typingSignals.SignalToolStart()
				}
				tool, exists := activeTools[streamEvent.ItemID]
				if !exists {
					callID := streamEvent.ItemID
					if strings.TrimSpace(callID) == "" {
						callID = NewCallID()
					}
					tool = &activeToolCall{
						callID:      callID,
						toolName:    streamEvent.Name,
						toolType:    ToolTypeFunction,
						startedAtMs: time.Now().UnixMilli(),
					}
					activeTools[streamEvent.ItemID] = tool
					if state.initialEventID == "" && !state.suppressSend {
						oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
					}
					if strings.TrimSpace(tool.toolName) != "" {
						tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
					}
				}
				tool.input.WriteString(streamEvent.Delta)
				oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, streamEvent.Name, streamEvent.Delta, tool.toolType == ToolTypeProvider)

			case "response.function_call_arguments.done":
				touchTyping()
				if typingSignals != nil {
					typingSignals.SignalToolStart()
				}
				tool, exists := activeTools[streamEvent.ItemID]
				if !exists {
					callID := streamEvent.ItemID
					if strings.TrimSpace(callID) == "" {
						callID = NewCallID()
					}
					tool = &activeToolCall{
						callID:      callID,
						toolName:    streamEvent.Name,
						toolType:    ToolTypeFunction,
						startedAtMs: time.Now().UnixMilli(),
					}
					tool.input.WriteString(streamEvent.Arguments)
					activeTools[streamEvent.ItemID] = tool
				}

				tool.itemID = streamEvent.ItemID

				toolName := strings.TrimSpace(tool.toolName)
				if toolName == "" {
					toolName = strings.TrimSpace(streamEvent.Name)
				}
				tool.toolName = toolName
				if tool.eventID == "" {
					tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
				}
				argsJSON := strings.TrimSpace(tool.input.String())
				if argsJSON == "" {
					argsJSON = strings.TrimSpace(streamEvent.Arguments)
				}
				argsJSON = normalizeToolArgsJSON(argsJSON)
				var inputMap any
				if err := json.Unmarshal([]byte(argsJSON), &inputMap); err != nil {
					inputMap = argsJSON
					oc.emitUIToolInputError(ctx, portal, state, tool.callID, toolName, argsJSON, "Invalid JSON tool input", tool.toolType == ToolTypeProvider, false)
				}
				oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, toolName, inputMap, tool.toolType == ToolTypeProvider)

				resultStatus := ResultStatusSuccess
				var result string
				if !oc.isToolEnabled(meta, toolName) {
					resultStatus = ResultStatusError
					result = fmt.Sprintf("Error: tool %s is disabled", toolName)
				} else {
					// Tool approval gating for dangerous builtin tools.
					argsObj, _ := inputMap.(map[string]any)
					required, action := oc.builtinToolApprovalRequirement(toolName, argsObj)
					if required && oc.isBuiltinAlwaysAllowed(toolName, action) {
						required = false
					}
					if required && state.heartbeat != nil {
						required = false
					}
					if required {
						approvalID := NewCallID()
						ttl := time.Duration(oc.toolApprovalsTTLSeconds()) * time.Second
						oc.registerToolApproval(struct {
							ApprovalID   string
							RoomID       id.RoomID
							TurnID       string
							ToolCallID   string
							ToolName     string
							ToolKind     ToolApprovalKind
							RuleToolName string
							ServerLabel  string
							Action       string
							TTL          time.Duration
						}{
							ApprovalID:   approvalID,
							RoomID:       state.roomID,
							TurnID:       state.turnID,
							ToolCallID:   tool.callID,
							ToolName:     toolName,
							ToolKind:     ToolApprovalKindBuiltin,
							RuleToolName: toolName,
							Action:       action,
							TTL:          ttl,
						})
						oc.emitUIToolApprovalRequest(ctx, portal, state, approvalID, tool.callID, toolName, tool.eventID, oc.toolApprovalsTTLSeconds())
						decision, _, ok := oc.waitToolApproval(ctx, approvalID)
						if !ok {
							if oc.toolApprovalsAskFallback() == "allow" {
								decision = ToolApprovalDecision{Approve: true, Reason: "fallback"}
							} else {
								decision = ToolApprovalDecision{Approve: false, Reason: "timeout"}
							}
						}
						if !decision.Approve {
							resultStatus = ResultStatusDenied
							result = "Denied by user"
							oc.emitUIToolOutputDenied(ctx, portal, state, tool.callID)
						}
					}

					if resultStatus != ResultStatusDenied {
						toolCtx := WithBridgeToolContext(ctx, &BridgeToolContext{
							Client:        oc,
							Portal:        portal,
							Meta:          meta,
							SourceEventID: state.sourceEventID,
							SenderID:      state.senderID,
						})
						var err error
						result, err = oc.executeBuiltinTool(toolCtx, portal, toolName, argsJSON)
						if err != nil {
							log.Warn().Err(err).Str("tool", toolName).Msg("Tool execution failed (continuation)")
							result = fmt.Sprintf("Error: %s", err.Error())
							resultStatus = ResultStatusError
						}
					}
				}

				// Check for TTS audio result (AUDIO: prefix)
				displayResult := result
				if strings.HasPrefix(result, TTSResultPrefix) {
					audioB64 := strings.TrimPrefix(result, TTSResultPrefix)
					audioData, err := base64.StdEncoding.DecodeString(audioB64)
					if err != nil {
						log.Warn().Err(err).Msg("Failed to decode TTS audio (continuation)")
						displayResult = "Error: failed to decode TTS audio"
						resultStatus = ResultStatusError
					} else {
						mimeType := detectAudioMime(audioData, "audio/mpeg")
						if _, mediaURL, err := oc.sendGeneratedAudio(ctx, portal, audioData, mimeType, state.turnID); err != nil {
							log.Warn().Err(err).Msg("Failed to send TTS audio (continuation)")
							displayResult = "Error: failed to send TTS audio"
							resultStatus = ResultStatusError
						} else {
							recordGeneratedFile(state, mediaURL, mimeType)
							oc.emitUIFile(ctx, portal, state, mediaURL, mimeType)
							displayResult = "Audio message sent successfully"
						}
					}
					result = displayResult
				}

				// Check for image generation result (IMAGE: / IMAGES: prefix)
				if strings.HasPrefix(result, ImagesResultPrefix) {
					payload := strings.TrimPrefix(result, ImagesResultPrefix)
					var images []string
					if err := json.Unmarshal([]byte(payload), &images); err != nil {
						log.Warn().Err(err).Msg("Failed to parse generated images payload (continuation)")
						displayResult = "Error: failed to parse generated images"
						resultStatus = ResultStatusError
					} else {
						success := 0
						for _, imageB64 := range images {
							imageData, mimeType, err := decodeBase64Image(imageB64)
							if err != nil {
								log.Warn().Err(err).Msg("Failed to decode generated image (continuation)")
								continue
							}
							_, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turnID)
							if err != nil {
								log.Warn().Err(err).Msg("Failed to send generated image (continuation)")
								continue
							}
							recordGeneratedFile(state, mediaURL, mimeType)
							oc.emitUIFile(ctx, portal, state, mediaURL, mimeType)
							success++
						}
						if success == len(images) && success > 0 {
							displayResult = fmt.Sprintf("Images generated and sent successfully (%d)", success)
						} else if success > 0 {
							displayResult = fmt.Sprintf("Images generated with %d/%d sent successfully", success, len(images))
							resultStatus = ResultStatusError
						} else {
							displayResult = "Error: failed to send generated images"
							resultStatus = ResultStatusError
						}
					}
					result = displayResult
				} else if strings.HasPrefix(result, ImageResultPrefix) {
					imageB64 := strings.TrimPrefix(result, ImageResultPrefix)
					imageData, mimeType, err := decodeBase64Image(imageB64)
					if err != nil {
						log.Warn().Err(err).Msg("Failed to decode generated image (continuation)")
						displayResult = "Error: failed to decode generated image"
						resultStatus = ResultStatusError
					} else {
						if _, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turnID); err != nil {
							log.Warn().Err(err).Msg("Failed to send generated image (continuation)")
							displayResult = "Error: failed to send generated image"
							resultStatus = ResultStatusError
						} else {
							recordGeneratedFile(state, mediaURL, mimeType)
							oc.emitUIFile(ctx, portal, state, mediaURL, mimeType)
							displayResult = "Image generated and sent successfully"
						}
					}
					result = displayResult
				}

				tool.result = result
				collectToolOutputCitations(state, toolName, result)
				state.pendingFunctionOutputs = append(state.pendingFunctionOutputs, functionCallOutput{
					callID:    streamEvent.ItemID,
					name:      toolName,
					arguments: argsJSON,
					output:    result,
				})

				// Emit UI tool output immediately so the desktop sees the tool
				// as completed without waiting for the timeline event send.
				if resultStatus == ResultStatusSuccess {
					oc.emitUIToolOutputAvailable(ctx, portal, state, tool.callID, result, tool.toolType == ToolTypeProvider, false)
				} else if resultStatus != ResultStatusDenied {
					oc.emitUIToolOutputError(ctx, portal, state, tool.callID, result, tool.toolType == ToolTypeProvider)
				}

				inputMapForMeta := map[string]any{}
				if parsed, ok := inputMap.(map[string]any); ok {
					inputMapForMeta = parsed
				} else if raw, ok := inputMap.(string); ok && raw != "" {
					inputMapForMeta = map[string]any{"_raw": raw}
				}

				// Track tool call in metadata (sendToolResultEvent is a blocking
				// Matrix API call, but the UI update was already emitted above).
				completedAt := time.Now().UnixMilli()
				resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, result, resultStatus)
				state.toolCalls = append(state.toolCalls, ToolCallMetadata{
					CallID:        tool.callID,
					ToolName:      toolName,
					ToolType:      string(tool.toolType),
					Input:         inputMapForMeta,
					Output:        map[string]any{"result": result},
					Status:        string(ToolStatusCompleted),
					ResultStatus:  string(resultStatus),
					StartedAtMs:   tool.startedAtMs,
					CompletedAtMs: completedAt,
					CallEventID:   string(tool.eventID),
					ResultEventID: string(resultEventID),
				})

			case "response.completed":
				state.completedAtMs = time.Now().UnixMilli()
				if streamEvent.Response.Usage.TotalTokens > 0 || streamEvent.Response.Usage.InputTokens > 0 || streamEvent.Response.Usage.OutputTokens > 0 {
					state.promptTokens = streamEvent.Response.Usage.InputTokens
					state.completionTokens = streamEvent.Response.Usage.OutputTokens
					state.reasoningTokens = streamEvent.Response.Usage.OutputTokensDetails.ReasoningTokens
					state.totalTokens = streamEvent.Response.Usage.TotalTokens
				}
				if streamEvent.Response.Status == "completed" {
					state.finishReason = "stop"
				} else {
					state.finishReason = string(streamEvent.Response.Status)
				}
				if streamEvent.Response.ID != "" {
					state.responseID = streamEvent.Response.ID
				}
				oc.emitUIMessageMetadata(ctx, portal, state, oc.buildUIMessageMetadata(state, meta, true))
				log.Debug().Str("reason", state.finishReason).Str("response_id", state.responseID).Msg("Continuation stream completed")

			case "error":
				apiErr := fmt.Errorf("API error: %s", streamEvent.Message)
				state.finishReason = "error"
				oc.emitUIError(ctx, portal, state, streamEvent.Message)
				oc.emitUIFinish(ctx, portal, state, meta)
				logResponsesFailure(log, apiErr, continuationParams, meta, messages, "continuation_event_error")
				if state.initialEventID != "" {
					return false, nil, &NonFallbackError{Err: apiErr}
				}
				return false, nil, &PreDeltaError{Err: apiErr}
			default:
				// Ignore unknown events
			}
		}

		oc.emitUIStepFinish(ctx, portal, state)

		if err := stream.Err(); err != nil {
			logResponsesFailure(log, err, continuationParams, meta, messages, "continuation_err")
			if errors.Is(err, context.Canceled) {
				state.finishReason = "cancelled"
				if state.initialEventID != "" && state.accumulated.Len() > 0 {
					oc.flushPartialStreamingMessage(context.Background(), portal, state, meta)
				}
				oc.emitUIAbort(ctx, portal, state, "cancelled")
				oc.emitUIFinish(ctx, portal, state, meta)
				if state.initialEventID != "" {
					return false, nil, &NonFallbackError{Err: err}
				}
				return false, nil, &PreDeltaError{Err: err}
			}
			state.finishReason = "error"
			oc.emitUIError(ctx, portal, state, err.Error())
			oc.emitUIFinish(ctx, portal, state, meta)
			if state.initialEventID != "" {
				return false, nil, &NonFallbackError{Err: err}
			}
			return false, nil, &PreDeltaError{Err: err}
		}
	}

	if state.finishReason == "" {
		state.finishReason = "stop"
	}

	// Send any generated images as separate messages
	for _, img := range state.pendingImages {
		imageData, mimeType, err := decodeBase64Image(img.imageB64)
		if err != nil {
			log.Warn().Err(err).Str("item_id", img.itemID).Msg("Failed to decode generated image")
			continue
		}
		eventID, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, img.turnID)
		if err != nil {
			log.Warn().Err(err).Str("item_id", img.itemID).Msg("Failed to send generated image to Matrix")
			continue
		}
		recordGeneratedFile(state, mediaURL, mimeType)
		oc.emitUIFile(ctx, portal, state, mediaURL, mimeType)
		log.Info().Stringer("event_id", eventID).Str("item_id", img.itemID).Msg("Sent generated image to Matrix")
	}
	oc.emitUIFinish(ctx, portal, state, meta)

	// Send final message to persist complete content with metadata (including reasoning)
	if state.initialEventID != "" || state.heartbeat != nil {
		oc.sendFinalAssistantTurn(ctx, portal, state, meta)
		if state.initialEventID != "" && !state.suppressSave {
			oc.saveAssistantMessage(ctx, log, portal, state, meta)
		}
	}

	log.Info().
		Str("turn_id", state.turnID).
		Str("finish_reason", state.finishReason).
		Int("content_length", state.accumulated.Len()).
		Int("reasoning_length", state.reasoning.Len()).
		Int("tool_calls", len(state.toolCalls)).
		Str("response_id", state.responseID).
		Int("images_sent", len(state.pendingImages)).
		Msg("Responses API streaming finished")

	// Generate room title after first response
	oc.maybeGenerateTitle(ctx, portal, state.accumulated.String())

	oc.recordProviderSuccess(ctx)
	return true, nil, nil
}

// buildContinuationParams builds params for continuing a response after tool execution
// and/or after responding to tool approval requests.
func (oc *AIClient) buildContinuationParams(
	ctx context.Context,
	state *streamingState,
	meta *PortalMetadata,
	pendingOutputs []functionCallOutput,
	approvalInputs []responses.ResponseInputItemUnionParam,
) responses.ResponseNewParams {
	params := responses.ResponseNewParams{
		Model:           shared.ResponsesModel(oc.effectiveModelForAPI(meta)),
		MaxOutputTokens: openai.Int(int64(oc.effectiveMaxTokens(meta))),
	}

	if systemPrompt := oc.effectivePrompt(meta); systemPrompt != "" {
		params.Instructions = openai.String(systemPrompt)
	}

	isOpenRouter := oc.isOpenRouterProvider()
	if !isOpenRouter {
		params.PreviousResponseID = openai.String(state.responseID)
	}

	// Build function call outputs as input
	var input responses.ResponseInputParam
	if isOpenRouter && len(state.baseInput) > 0 {
		// OpenRouter Responses API is stateless: include full history plus tool calls.
		input = append(input, state.baseInput...)
	}
	for _, approval := range approvalInputs {
		input = append(input, approval)
	}
	for _, output := range pendingOutputs {
		if isOpenRouter && output.name != "" {
			args := output.arguments
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			input = append(input, responses.ResponseInputItemParamOfFunctionCall(args, output.callID, output.name))
		}
		input = append(input, buildFunctionCallOutputItem(output.callID, output.output, isOpenRouter))
	}
	steerItems := oc.drainSteerQueue(state.roomID)
	if len(steerItems) > 0 {
		steerInput := oc.buildSteerInputItems(steerItems, meta)
		if len(steerInput) > 0 {
			input = append(input, steerInput...)
			if isOpenRouter && len(state.baseInput) > 0 {
				state.baseInput = append(state.baseInput, steerInput...)
			}
		}
	}
	params.Input = responses.ResponseNewParamsInputUnion{
		OfInputItemList: input,
	}

	// Add reasoning effort if configured
	if reasoningEffort := oc.effectiveReasoningEffort(meta); reasoningEffort != "" {
		params.Reasoning = shared.ReasoningParam{
			Effort: shared.ReasoningEffort(reasoningEffort),
		}
	}

	// Add builtin function tools only for agent chats that support tool calling.
	// Model-only chats use a simple prompt without tools to avoid context overflow on small models.
	agentID := resolveAgentID(meta)
	if meta.Capabilities.SupportsToolCalling && agentID != "" {
		enabledTools := oc.enabledBuiltinToolsForModel(ctx, meta)
		if len(enabledTools) > 0 {
			strictMode := resolveToolStrictMode(oc.isOpenRouterProvider())
			params.Tools = append(params.Tools, ToOpenAITools(enabledTools, strictMode, &oc.log)...)
		}
	}

	// Add boss tools for Boss agent rooms (needed for multi-turn tool use)
	if hasBossAgent(meta) || agents.IsBossAgent(agentID) {
		var enabledBoss []*tools.Tool
		for _, tool := range tools.BossTools() {
			if oc.isToolEnabled(meta, tool.Name) {
				enabledBoss = append(enabledBoss, tool)
			}
		}
		strictMode := resolveToolStrictMode(oc.isOpenRouterProvider())
		params.Tools = append(params.Tools, bossToolsToOpenAI(enabledBoss, strictMode, &oc.log)...)
	}

	// Add session tools for non-boss agent rooms (needed for multi-turn tool use)
	if meta.Capabilities.SupportsToolCalling && agentID != "" && !(hasBossAgent(meta) || agents.IsBossAgent(agentID)) {
		var enabledSessions []*tools.Tool
		for _, tool := range tools.SessionTools() {
			if oc.isToolEnabled(meta, tool.Name) {
				enabledSessions = append(enabledSessions, tool)
			}
		}
		if len(enabledSessions) > 0 {
			strictMode := resolveToolStrictMode(oc.isOpenRouterProvider())
			params.Tools = append(params.Tools, bossToolsToOpenAI(enabledSessions, strictMode, &oc.log)...)
		}
	}

	if oc.isOpenRouterProvider() {
		params.Tools = renameWebSearchToolParams(params.Tools)
	}

	// Prevent duplicate tool names (Anthropic rejects duplicates)
	params.Tools = dedupeToolParams(params.Tools)
	logToolParamDuplicates(&oc.log, params.Tools)

	return params
}

func (oc *AIClient) buildSteerInputItems(items []pendingQueueItem, meta *PortalMetadata) responses.ResponseInputParam {
	if oc == nil || len(items) == 0 {
		return nil
	}
	var input responses.ResponseInputParam
	for _, item := range items {
		if item.pending.Type != pendingTypeText {
			continue
		}
		prompt := strings.TrimSpace(item.prompt)
		if prompt == "" {
			prompt = item.pending.MessageBody
			if item.pending.Event != nil {
				prompt = appendMessageIDHint(prompt, item.pending.Event.ID)
			}
		}
		prompt = strings.TrimSpace(prompt)
		if prompt == "" {
			continue
		}
		messages := []openai.ChatCompletionMessageParamUnion{openai.UserMessage(prompt)}
		input = append(input, oc.convertToResponsesInput(messages, meta)...)
	}
	return input
}

// streamChatCompletions handles streaming using Chat Completions API (for audio support)
// This is used as a fallback when the prompt contains audio content, since
// SDK v3.16.0 has ResponseInputAudioParam defined but NOT wired into ResponseInputContentUnionParam.
func (oc *AIClient) streamChatCompletions(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	messages []openai.ChatCompletionMessageParamUnion,
) (bool, *ContextLengthError, error) {
	portalID := ""
	if portal != nil {
		portalID = string(portal.ID)
	}
	log := zerolog.Ctx(ctx).With().
		Str("action", "stream_chat_completions").
		Str("portal", portalID).
		Logger()

	// Initialize streaming state with source event ID for [[reply_to_current]] support
	var sourceEventID id.EventID
	senderID := ""
	if evt != nil {
		sourceEventID = evt.ID
		if evt.Sender != "" {
			senderID = evt.Sender.String()
		}
	}
	roomID := id.RoomID("")
	if portal != nil {
		roomID = portal.MXID
	}
	state := newStreamingState(ctx, meta, sourceEventID, senderID, roomID)
	state.replyTarget = oc.resolveInitialReplyTarget(evt)

	// Ensure model ghost is in the room before any operations
	if !state.suppressSend {
		if err := oc.ensureModelInRoom(ctx, portal); err != nil {
			log.Warn().Err(err).Msg("Failed to ensure model is in room")
			// Continue anyway - typing will fail gracefully
		}
	}

	// Create typing controller with TTL and automatic refresh
	var typingCtrl *TypingController
	var typingSignals *TypingSignaler
	touchTyping := func() {}
	isHeartbeat := state.heartbeat != nil
	if !state.suppressSend && !isHeartbeat {
		mode := oc.resolveTypingMode(meta, typingContextFromContext(ctx), isHeartbeat)
		interval := oc.resolveTypingInterval(meta)
		if interval > 0 && mode != TypingModeNever {
			typingCtrl = NewTypingController(oc, ctx, portal, TypingControllerOptions{
				Interval: interval,
				TTL:      typingTTL,
			})
			typingSignals = NewTypingSignaler(typingCtrl, mode, isHeartbeat)
			touchTyping = func() {
				typingCtrl.RefreshTTL()
			}
		}
	}
	if typingSignals != nil {
		typingSignals.SignalRunStart()
	}
	defer func() {
		if typingCtrl != nil {
			typingCtrl.MarkRunComplete()
			typingCtrl.MarkDispatchIdle()
		}
	}()

	// Apply proactive context pruning if enabled
	messages = oc.applyProactivePruning(ctx, messages, meta)

	currentMessages := messages
	maxToolRounds := 3

	oc.emitUIStart(ctx, portal, state, meta)

	for round := 0; ; round++ {
		params := openai.ChatCompletionNewParams{
			Model:    oc.effectiveModelForAPI(meta),
			Messages: currentMessages,
		}
		params.StreamOptions = openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: param.NewOpt(true),
		}
		if maxTokens := oc.effectiveMaxTokens(meta); maxTokens > 0 {
			params.MaxCompletionTokens = openai.Int(int64(maxTokens))
		}
		if temp := oc.effectiveTemperature(meta); temp > 0 {
			params.Temperature = openai.Float(temp)
		}
		// Add tools only for agent chats that support tool calling.
		// Model-only chats use a simple prompt without tools to avoid context overflow on small models.
		chatHasAgent := resolveAgentID(meta) != ""
		if meta.Capabilities.SupportsToolCalling && chatHasAgent {
			enabledTools := oc.enabledBuiltinToolsForModel(ctx, meta)
			if len(enabledTools) > 0 {
				params.Tools = append(params.Tools, ToOpenAIChatTools(enabledTools, &oc.log)...)
			}
			if !oc.isBuilderRoom(portal) {
				var enabledSessions []*tools.Tool
				for _, tool := range tools.SessionTools() {
					if oc.isToolEnabled(meta, tool.Name) {
						enabledSessions = append(enabledSessions, tool)
					}
				}
				if len(enabledSessions) > 0 {
					params.Tools = append(params.Tools, bossToolsToChatTools(enabledSessions, &oc.log)...)
				}
			}
			if hasBossAgent(meta) || oc.isBuilderRoom(portal) {
				var enabledBoss []*tools.Tool
				for _, tool := range tools.BossTools() {
					if oc.isToolEnabled(meta, tool.Name) {
						enabledBoss = append(enabledBoss, tool)
					}
				}
				params.Tools = append(params.Tools, bossToolsToChatTools(enabledBoss, &oc.log)...)
			}
			params.Tools = dedupeChatToolParams(params.Tools)
		}

		stream := oc.api.Chat.Completions.NewStreaming(ctx, params)
		if stream == nil {
			initErr := errors.New("chat completions streaming not available")
			logChatCompletionsFailure(log, initErr, params, meta, currentMessages, "stream_init")
			return false, nil, &PreDeltaError{Err: initErr}
		}

		// Track active tool calls by index
		activeTools := make(map[int]*activeToolCall)
		var roundContent strings.Builder
		state.finishReason = ""

		oc.emitUIStepStart(ctx, portal, state)

		for stream.Next() {
			chunk := stream.Current()
			oc.markMessageSendSuccess(ctx, portal, evt, state)

			if chunk.Usage.TotalTokens > 0 || chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
				state.promptTokens = chunk.Usage.PromptTokens
				state.completionTokens = chunk.Usage.CompletionTokens
				state.reasoningTokens = chunk.Usage.CompletionTokensDetails.ReasoningTokens
				state.totalTokens = chunk.Usage.TotalTokens
				oc.emitUIMessageMetadata(ctx, portal, state, oc.buildUIMessageMetadata(state, meta, true))
			}

			for _, choice := range chunk.Choices {
				if choice.Delta.Content != "" {
					touchTyping()
					state.accumulated.WriteString(choice.Delta.Content)
					roundContent.WriteString(choice.Delta.Content)

					parsed := (*streamingDirectiveResult)(nil)
					if state.replyAccumulator != nil {
						parsed = state.replyAccumulator.Consume(choice.Delta.Content, false)
					}
					if parsed != nil {
						oc.applyStreamingReplyTarget(state, parsed)
						cleaned := parsed.Text
						if typingSignals != nil {
							typingSignals.SignalTextDelta(cleaned)
						}
						if cleaned != "" {
							state.visibleAccumulated.WriteString(cleaned)
							if state.firstToken && state.visibleAccumulated.Len() > 0 {
								state.firstToken = false
								state.firstTokenAtMs = time.Now().UnixMilli()
								if !state.suppressSend && !isHeartbeat {
									oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
									state.initialEventID = oc.sendInitialStreamMessage(ctx, portal, state.visibleAccumulated.String(), state.turnID, state.replyTarget)
									if state.initialEventID == "" {
										errText := "failed to send initial streaming message"
										log.Error().Msg("Failed to send initial streaming message")
										state.finishReason = "error"
										oc.emitUIError(ctx, portal, state, errText)
										oc.emitUIFinish(ctx, portal, state, meta)
										return false, nil, &PreDeltaError{Err: errors.New(errText)}
									}
								}
							}
							oc.emitUITextDelta(ctx, portal, state, cleaned)
						}
					}
				}

				if choice.Delta.Refusal != "" {
					touchTyping()
					if typingSignals != nil {
						typingSignals.SignalTextDelta(choice.Delta.Refusal)
					}
					oc.emitUITextDelta(ctx, portal, state, choice.Delta.Refusal)
				}

				// Handle tool calls from Chat Completions API
				for _, toolDelta := range choice.Delta.ToolCalls {
					touchTyping()
					if typingSignals != nil {
						typingSignals.SignalToolStart()
					}
					toolIdx := int(toolDelta.Index)
					tool, exists := activeTools[toolIdx]
					if !exists {
						callID := toolDelta.ID
						if strings.TrimSpace(callID) == "" {
							callID = NewCallID()
						}
						tool = &activeToolCall{
							callID:      callID,
							toolType:    ToolTypeFunction,
							startedAtMs: time.Now().UnixMilli(),
						}
						activeTools[toolIdx] = tool
					}

					// Capture tool ID if provided (used by OpenAI for tracking)
					if toolDelta.ID != "" && tool.callID == "" {
						tool.callID = toolDelta.ID
					}

					// Update tool name if provided in this delta
					if toolDelta.Function.Name != "" {
						tool.toolName = toolDelta.Function.Name
						if tool.eventID == "" {
							tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
						}
					}

					// Accumulate arguments
					if toolDelta.Function.Arguments != "" {
						tool.input.WriteString(toolDelta.Function.Arguments)
						oc.emitUIToolInputDelta(ctx, portal, state, tool.callID, tool.toolName, toolDelta.Function.Arguments, false)
					}
				}

				if choice.FinishReason != "" {
					state.finishReason = string(choice.FinishReason)
				}
			}

		}

		oc.emitUIStepFinish(ctx, portal, state)

		if err := stream.Err(); err != nil {
			if errors.Is(err, context.Canceled) {
				state.finishReason = "cancelled"
				if state.initialEventID != "" && state.accumulated.Len() > 0 {
					oc.flushPartialStreamingMessage(context.Background(), portal, state, meta)
				}
				oc.emitUIAbort(ctx, portal, state, "cancelled")
				oc.emitUIFinish(ctx, portal, state, meta)
				if state.initialEventID != "" {
					return false, nil, &NonFallbackError{Err: err}
				}
				return false, nil, &PreDeltaError{Err: err}
			}
			if cle := ParseContextLengthError(err); cle != nil {
				return false, cle, nil
			}
			logChatCompletionsFailure(log, err, params, meta, currentMessages, "stream_err")
			state.finishReason = "error"
			oc.emitUIError(ctx, portal, state, err.Error())
			oc.emitUIFinish(ctx, portal, state, meta)
			if state.initialEventID != "" {
				return false, nil, &NonFallbackError{Err: err}
			}
			return false, nil, &PreDeltaError{Err: err}
		}

		// Execute any accumulated tool calls
		type chatToolResult struct {
			callID string
			output string
		}
		toolCallParams := make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(activeTools))
		toolResults := make([]chatToolResult, 0, len(activeTools))

		if len(activeTools) > 0 {
			keys := make([]int, 0, len(activeTools))
			for key := range activeTools {
				keys = append(keys, key)
			}
			sort.Ints(keys)
			for _, key := range keys {
				tool := activeTools[key]
				if tool == nil {
					continue
				}
				if tool.callID == "" {
					tool.callID = NewCallID()
				}
				toolName := strings.TrimSpace(tool.toolName)
				if toolName == "" {
					toolName = "unknown_tool"
				}
				if tool.eventID == "" {
					tool.toolName = toolName
					tool.eventID = oc.sendToolCallEvent(ctx, portal, state, tool)
				}

				argsJSON := normalizeToolArgsJSON(tool.input.String())
				toolCallParams = append(toolCallParams, openai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
						ID: tool.callID,
						Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
							Name:      toolName,
							Arguments: argsJSON,
						},
						Type: constant.ValueOf[constant.Function](),
					},
				})

				touchTyping()
				if typingSignals != nil {
					typingSignals.SignalToolStart()
				}
				// Wrap context with bridge info for tools that need it (e.g., channel-edit, react)
				toolCtx := WithBridgeToolContext(ctx, &BridgeToolContext{
					Client:        oc,
					Portal:        portal,
					Meta:          meta,
					SourceEventID: state.sourceEventID,
					SenderID:      state.senderID,
				})

				result := ""
				resultStatus := ResultStatusSuccess
				if !oc.isToolEnabled(meta, toolName) {
					result = fmt.Sprintf("Error: tool %s is not enabled", toolName)
					resultStatus = ResultStatusError
				} else {
					// Tool approval gating for dangerous builtin tools.
					var argsObj map[string]any
					_ = json.Unmarshal([]byte(argsJSON), &argsObj)
					required, action := oc.builtinToolApprovalRequirement(toolName, argsObj)
					if required && oc.isBuiltinAlwaysAllowed(toolName, action) {
						required = false
					}
					if required && state.heartbeat != nil {
						required = false
					}
					if required {
						approvalID := NewCallID()
						ttl := time.Duration(oc.toolApprovalsTTLSeconds()) * time.Second
						oc.registerToolApproval(struct {
							ApprovalID   string
							RoomID       id.RoomID
							TurnID       string
							ToolCallID   string
							ToolName     string
							ToolKind     ToolApprovalKind
							RuleToolName string
							ServerLabel  string
							Action       string
							TTL          time.Duration
						}{
							ApprovalID:   approvalID,
							RoomID:       state.roomID,
							TurnID:       state.turnID,
							ToolCallID:   tool.callID,
							ToolName:     toolName,
							ToolKind:     ToolApprovalKindBuiltin,
							RuleToolName: toolName,
							Action:       action,
							TTL:          ttl,
						})
						oc.emitUIToolApprovalRequest(ctx, portal, state, approvalID, tool.callID, toolName, tool.eventID, oc.toolApprovalsTTLSeconds())
						decision, _, ok := oc.waitToolApproval(ctx, approvalID)
						if !ok {
							if oc.toolApprovalsAskFallback() == "allow" {
								decision = ToolApprovalDecision{Approve: true, Reason: "fallback"}
							} else {
								decision = ToolApprovalDecision{Approve: false, Reason: "timeout"}
							}
						}
						if !decision.Approve {
							resultStatus = ResultStatusDenied
							result = "Denied by user"
							oc.emitUIToolOutputDenied(ctx, portal, state, tool.callID)
						}
					}

					if resultStatus != ResultStatusDenied {
						var err error
						result, err = oc.executeBuiltinTool(toolCtx, portal, toolName, argsJSON)
						if err != nil {
							log.Warn().Err(err).Str("tool", toolName).Msg("Tool execution failed (Chat Completions)")
							result = fmt.Sprintf("Error: %s", err.Error())
							resultStatus = ResultStatusError
						}
					}

					// Check for TTS audio result (AUDIO: prefix)
					if strings.HasPrefix(result, TTSResultPrefix) {
						audioB64 := strings.TrimPrefix(result, TTSResultPrefix)
						audioData, decodeErr := base64.StdEncoding.DecodeString(audioB64)
						if decodeErr != nil {
							log.Warn().Err(decodeErr).Msg("Failed to decode TTS audio (Chat Completions)")
							result = "Error: failed to decode TTS audio"
							resultStatus = ResultStatusError
						} else {
							mimeType := detectAudioMime(audioData, "audio/mpeg")
							if _, mediaURL, sendErr := oc.sendGeneratedAudio(ctx, portal, audioData, mimeType, state.turnID); sendErr != nil {
								log.Warn().Err(sendErr).Msg("Failed to send TTS audio (Chat Completions)")
								result = "Error: failed to send TTS audio"
								resultStatus = ResultStatusError
							} else {
								recordGeneratedFile(state, mediaURL, mimeType)
								oc.emitUIFile(ctx, portal, state, mediaURL, mimeType)
								result = "Audio message sent successfully"
							}
						}
					}

					// Check for image generation result (IMAGE: / IMAGES: prefix)
					if strings.HasPrefix(result, ImagesResultPrefix) {
						payload := strings.TrimPrefix(result, ImagesResultPrefix)
						var images []string
						if err := json.Unmarshal([]byte(payload), &images); err != nil {
							log.Warn().Err(err).Msg("Failed to parse generated images payload (Chat Completions)")
							result = "Error: failed to parse generated images"
							resultStatus = ResultStatusError
						} else {
							success := 0
							for _, imageB64 := range images {
								imageData, mimeType, decodeErr := decodeBase64Image(imageB64)
								if decodeErr != nil {
									log.Warn().Err(decodeErr).Msg("Failed to decode generated image (Chat Completions)")
									continue
								}
								_, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turnID)
								if err != nil {
									log.Warn().Err(err).Msg("Failed to send generated image (Chat Completions)")
									continue
								}
								recordGeneratedFile(state, mediaURL, mimeType)
								oc.emitUIFile(ctx, portal, state, mediaURL, mimeType)
								success++
							}
							if success == len(images) && success > 0 {
								result = fmt.Sprintf("Images generated and sent successfully (%d)", success)
							} else if success > 0 {
								result = fmt.Sprintf("Images generated with %d/%d sent successfully", success, len(images))
								resultStatus = ResultStatusError
							} else {
								result = "Error: failed to send generated images"
								resultStatus = ResultStatusError
							}
						}
					} else if strings.HasPrefix(result, ImageResultPrefix) {
						imageB64 := strings.TrimPrefix(result, ImageResultPrefix)
						imageData, mimeType, decodeErr := decodeBase64Image(imageB64)
						if decodeErr != nil {
							log.Warn().Err(decodeErr).Msg("Failed to decode generated image (Chat Completions)")
							result = "Error: failed to decode generated image"
							resultStatus = ResultStatusError
						} else {
							if _, mediaURL, sendErr := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, state.turnID); sendErr != nil {
								log.Warn().Err(sendErr).Msg("Failed to send generated image (Chat Completions)")
								result = "Error: failed to send generated image"
								resultStatus = ResultStatusError
							} else {
								recordGeneratedFile(state, mediaURL, mimeType)
								oc.emitUIFile(ctx, portal, state, mediaURL, mimeType)
								result = "Image generated and sent successfully"
							}
						}
					}
				}

				// Normalize input for storage
				var inputMap any
				if err := json.Unmarshal([]byte(argsJSON), &inputMap); err != nil {
					inputMap = argsJSON
					oc.emitUIToolInputError(ctx, portal, state, tool.callID, toolName, argsJSON, "Invalid JSON tool input", false, false)
				}
				inputMapForMeta := map[string]any{}
				if parsed, ok := inputMap.(map[string]any); ok {
					inputMapForMeta = parsed
				} else if raw, ok := inputMap.(string); ok && raw != "" {
					inputMapForMeta = map[string]any{"_raw": raw}
				}
				oc.emitUIToolInputAvailable(ctx, portal, state, tool.callID, toolName, inputMap, false)

				// Track tool call in metadata
				completedAt := time.Now().UnixMilli()
				resultEventID := oc.sendToolResultEvent(ctx, portal, state, tool, result, resultStatus)
				state.toolCalls = append(state.toolCalls, ToolCallMetadata{
					CallID:        tool.callID,
					ToolName:      toolName,
					ToolType:      string(tool.toolType),
					Input:         inputMapForMeta,
					Output:        map[string]any{"result": result},
					Status:        string(ToolStatusCompleted),
					ResultStatus:  string(resultStatus),
					StartedAtMs:   tool.startedAtMs,
					CompletedAtMs: completedAt,
					CallEventID:   string(tool.eventID),
					ResultEventID: string(resultEventID),
				})

				if resultStatus == ResultStatusSuccess {
					collectToolOutputCitations(state, toolName, result)
					oc.emitUIToolOutputAvailable(ctx, portal, state, tool.callID, result, tool.toolType == ToolTypeProvider, false)
				} else if resultStatus != ResultStatusDenied {
					oc.emitUIToolOutputError(ctx, portal, state, tool.callID, result, tool.toolType == ToolTypeProvider)
				}

				toolResults = append(toolResults, chatToolResult{callID: tool.callID, output: result})
			}
		}

		// Continue if tools were requested.
		// Some Anthropic-compatible adapters may emit `tool_use` (or omit finish reason)
		// even when tool calls are present.
		if shouldContinueChatToolLoop(state.finishReason, len(toolCallParams)) {
			if round >= maxToolRounds {
				log.Warn().Int("rounds", round+1).Msg("Max tool call rounds reached; stopping chat completions continuation")
				break
			}
			assistantMsg := openai.ChatCompletionAssistantMessageParam{
				ToolCalls: toolCallParams,
			}
			if content := strings.TrimSpace(roundContent.String()); content != "" {
				assistantMsg.Content.OfString = param.NewOpt(content)
			}
			currentMessages = append(currentMessages, openai.ChatCompletionMessageParamUnion{OfAssistant: &assistantMsg})
			for _, result := range toolResults {
				currentMessages = append(currentMessages, openai.ToolMessage(result.output, result.callID))
			}
			if steerItems := oc.drainSteerQueue(state.roomID); len(steerItems) > 0 {
				for _, item := range steerItems {
					if item.pending.Type != pendingTypeText {
						continue
					}
					prompt := strings.TrimSpace(item.prompt)
					if prompt == "" {
						prompt = item.pending.MessageBody
						if item.pending.Event != nil {
							prompt = appendMessageIDHint(prompt, item.pending.Event.ID)
						}
					}
					prompt = strings.TrimSpace(prompt)
					if prompt == "" {
						continue
					}
					currentMessages = append(currentMessages, openai.UserMessage(prompt))
				}
			}
			continue
		}

		break
	}

	state.completedAtMs = time.Now().UnixMilli()
	if state.finishReason == "" {
		state.finishReason = "stop"
	}
	oc.emitUIFinish(ctx, portal, state, meta)

	// Send final edit and save to database
	if state.initialEventID != "" {
		oc.sendFinalAssistantTurn(ctx, portal, state, meta)
		if !state.suppressSave {
			oc.saveAssistantMessage(ctx, log, portal, state, meta)
		}
	}

	log.Info().
		Str("turn_id", state.turnID).
		Str("finish_reason", state.finishReason).
		Int("content_length", state.accumulated.Len()).
		Int("tool_calls", len(state.toolCalls)).
		Msg("Chat Completions streaming finished")

	oc.maybeGenerateTitle(ctx, portal, state.accumulated.String())
	oc.recordProviderSuccess(ctx)
	return true, nil, nil
}

// convertToResponsesInput converts Chat Completion messages to Responses API input items
// Supports native multimodal content: images (ResponseInputImageParam), files/PDFs (ResponseInputFileParam)
// Note: Audio is handled via Chat Completions API fallback (SDK v3.16.0 lacks Responses API audio union support)
func (oc *AIClient) convertToResponsesInput(messages []openai.ChatCompletionMessageParamUnion, _ *PortalMetadata) responses.ResponseInputParam {
	var input responses.ResponseInputParam

	for _, msg := range messages {
		if msg.OfTool != nil {
			toolCallID := strings.TrimSpace(msg.OfTool.ToolCallID)
			content := strings.TrimSpace(extractToolContent(msg.OfTool.Content))
			if toolCallID != "" && content != "" {
				input = append(input, responses.ResponseInputItemUnionParam{
					OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
						CallID: toolCallID,
						Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
							OfString: openai.String(content),
						},
					},
				})
			}
			continue
		}

		if msg.OfUser != nil {
			var contentParts responses.ResponseInputMessageContentListParam
			hasMultimodal := false
			textContent := ""

			if msg.OfUser.Content.OfString.Value != "" {
				textContent = msg.OfUser.Content.OfString.Value
			}

			if len(msg.OfUser.Content.OfArrayOfContentParts) > 0 {
				for _, part := range msg.OfUser.Content.OfArrayOfContentParts {
					if part.OfText != nil && part.OfText.Text != "" {
						if textContent != "" {
							textContent += "\n"
						}
						textContent += part.OfText.Text
					}
					if part.OfImageURL != nil && part.OfImageURL.ImageURL.URL != "" {
						hasMultimodal = true
						detail := responses.ResponseInputImageDetailAuto
						switch part.OfImageURL.ImageURL.Detail {
						case "low":
							detail = responses.ResponseInputImageDetailLow
						case "high":
							detail = responses.ResponseInputImageDetailHigh
						}
						contentParts = append(contentParts, responses.ResponseInputContentUnionParam{
							OfInputImage: &responses.ResponseInputImageParam{
								ImageURL: openai.String(part.OfImageURL.ImageURL.URL),
								Detail:   detail,
							},
						})
					}
					if part.OfFile != nil {
						fileData := part.OfFile.File.FileData.Value
						fileID := part.OfFile.File.FileID.Value
						filename := part.OfFile.File.Filename.Value
						if fileData == "" && fileID == "" {
							continue
						}
						hasMultimodal = true
						fileParam := &responses.ResponseInputFileParam{}
						if fileData != "" {
							fileParam.FileData = openai.String(fileData)
						}
						if fileID != "" {
							fileParam.FileID = openai.String(fileID)
						}
						if filename != "" {
							fileParam.Filename = openai.String(filename)
						}
						contentParts = append(contentParts, responses.ResponseInputContentUnionParam{
							OfInputFile: fileParam,
						})
					}
					// Note: Audio handled by Chat Completions fallback, skip here
				}
			}

			if textContent != "" {
				textPart := responses.ResponseInputContentUnionParam{
					OfInputText: &responses.ResponseInputTextParam{
						Text: textContent,
					},
				}
				contentParts = append([]responses.ResponseInputContentUnionParam{textPart}, contentParts...)
			}

			if hasMultimodal && len(contentParts) > 0 {
				input = append(input, responses.ResponseInputItemUnionParam{
					OfMessage: &responses.EasyInputMessageParam{
						Role: responses.EasyInputMessageRoleUser,
						Content: responses.EasyInputMessageContentUnionParam{
							OfInputItemContentList: contentParts,
						},
					},
				})
			} else if textContent != "" {
				input = append(input, responses.ResponseInputItemUnionParam{
					OfMessage: &responses.EasyInputMessageParam{
						Role: responses.EasyInputMessageRoleUser,
						Content: responses.EasyInputMessageContentUnionParam{
							OfString: openai.String(textContent),
						},
					},
				})
			}
			continue
		}

		content, role := extractMessageContent(msg)
		if role == "" || content == "" {
			continue
		}

		var responsesRole responses.EasyInputMessageRole
		switch role {
		case "system":
			responsesRole = responses.EasyInputMessageRoleSystem
		case "developer":
			responsesRole = responses.EasyInputMessageRoleDeveloper
		case "assistant":
			responsesRole = responses.EasyInputMessageRoleAssistant
		case "user":
			responsesRole = responses.EasyInputMessageRoleUser
		default:
			continue
		}

		input = append(input, responses.ResponseInputItemUnionParam{
			OfMessage: &responses.EasyInputMessageParam{
				Role: responsesRole,
				Content: responses.EasyInputMessageContentUnionParam{
					OfString: openai.String(content),
				},
			},
		})
	}

	return input
}

// hasAudioContent checks if the prompt contains audio content
func hasAudioContent(messages []openai.ChatCompletionMessageParamUnion) bool {
	for _, msg := range messages {
		if msg.OfUser != nil && len(msg.OfUser.Content.OfArrayOfContentParts) > 0 {
			for _, part := range msg.OfUser.Content.OfArrayOfContentParts {
				if part.OfInputAudio != nil {
					return true
				}
			}
		}
	}
	return false
}

// hasMultimodalContent checks if the prompt contains non-text content (image, file, audio).
func hasMultimodalContent(messages []openai.ChatCompletionMessageParamUnion) bool {
	for _, msg := range messages {
		if msg.OfUser != nil && len(msg.OfUser.Content.OfArrayOfContentParts) > 0 {
			for _, part := range msg.OfUser.Content.OfArrayOfContentParts {
				if part.OfImageURL != nil || part.OfFile != nil || part.OfInputAudio != nil {
					return true
				}
			}
		}
	}
	return false
}
