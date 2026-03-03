package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/matrixevents"
	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

// runTurn sends a user message to Codex and streams the response back to the portal.
func (cc *CodexClient) runTurn(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, sourceEvent *event.Event, body string) {
	log := cc.loggerForContext(ctx)
	state := newStreamingState(sourceEvent.ID)
	state.startedAtMs = time.Now().UnixMilli()

	model := cc.connector.Config.Codex.DefaultModel
	threadID := strings.TrimSpace(meta.CodexThreadID)
	cwd := strings.TrimSpace(meta.CodexCwd)

	state.initialEventID = cc.sendInitialStreamMessage(ctx, portal, state, "...", state.turnID)
	if !state.hasInitialMessageTarget() {
		log.Warn().Msg("Failed to send initial streaming message")
		return
	}
	cc.emitUIStart(ctx, portal, state, model)
	cc.uiEmitter(state).EmitUIStepStart(ctx, portal)

	approvalPolicy := "untrusted"
	if lvl, _ := stringutil.NormalizeElevatedLevel(meta.ElevatedLevel); lvl == "full" {
		approvalPolicy = "never"
	}

	var turnStart struct {
		Turn struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"turn"`
	}
	turnStartCtx, cancelTurnStart := context.WithTimeout(ctx, 60*time.Second)
	defer cancelTurnStart()
	err := cc.rpc.Call(turnStartCtx, "turn/start", map[string]any{
		"threadId": threadID,
		"input": []map[string]any{
			{"type": "text", "text": body},
		},
		"cwd":            cwd,
		"approvalPolicy": approvalPolicy,
		"sandboxPolicy":  cc.buildSandboxPolicy(cwd),
	}, &turnStart)
	if err != nil {
		cc.uiEmitter(state).EmitUIError(ctx, portal, err.Error())
		cc.emitUIFinish(ctx, portal, state, model, "failed")
		cc.sendFinalAssistantTurn(ctx, portal, state, model, "failed")
		cc.saveAssistantMessage(ctx, portal, state, model, "failed")
		return
	}
	turnID := strings.TrimSpace(turnStart.Turn.ID)
	if turnID == "" {
		turnID = "turn_unknown"
	}

	turnCh := cc.subscribeTurn(threadID, turnID)
	defer cc.unsubscribeTurn(threadID, turnID)

	cc.activeMu.Lock()
	cc.activeTurns[codexTurnKey(threadID, turnID)] = &codexActiveTurn{
		portal:   portal,
		meta:     meta,
		state:    state,
		threadID: threadID,
		turnID:   turnID,
		model:    model,
	}
	cc.activeMu.Unlock()
	defer func() {
		cc.activeMu.Lock()
		delete(cc.activeTurns, codexTurnKey(threadID, turnID))
		cc.activeMu.Unlock()
	}()

	finishStatus := "completed"
	var completedErr string
	maxWait := time.NewTimer(10 * time.Minute)
	defer maxWait.Stop()
	for {
		select {
		case evt := <-turnCh:
			cc.handleNotif(ctx, portal, meta, state, model, threadID, turnID, evt)
			if st, errText, ok := codexTurnCompletedStatus(evt, threadID, turnID); ok {
				finishStatus = st
				completedErr = errText
				goto done
			}
			maxWait.Reset(10 * time.Minute)
		case <-maxWait.C:
			finishStatus = "timeout"
			goto done
		case <-ctx.Done():
			finishStatus = "interrupted"
			goto done
		}
	}

done:
	log.Debug().Str("status", finishStatus).Str("thread", threadID).Str("turn", turnID).Msg("Codex turn finished")
	state.completedAtMs = time.Now().UnixMilli()
	if diff := strings.TrimSpace(state.codexLatestDiff); diff != "" {
		diffToolID := fmt.Sprintf("diff-%s", turnID)
		cc.ensureUIToolInputStart(ctx, portal, state, diffToolID, "diff", true, map[string]any{"turnId": turnID})
		cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, diffToolID, diff, true, false)
		state.toolCalls = append(state.toolCalls, ToolCallMetadata{
			CallID:        diffToolID,
			ToolName:      "diff",
			ToolType:      string(matrixevents.ToolTypeProvider),
			Input:         map[string]any{"turnId": turnID},
			Output:        map[string]any{"diff": diff},
			Status:        string(matrixevents.ToolStatusCompleted),
			ResultStatus:  string(matrixevents.ResultStatusSuccess),
			StartedAtMs:   state.startedAtMs,
			CompletedAtMs: state.completedAtMs,
		})
	}
	if completedErr != "" {
		cc.uiEmitter(state).EmitUIError(ctx, portal, completedErr)
	}
	cc.emitUIFinish(ctx, portal, state, model, finishStatus)
	cc.sendFinalAssistantTurn(ctx, portal, state, model, finishStatus)
	cc.saveAssistantMessage(ctx, portal, state, model, finishStatus)
	cc.markMessageSendSuccess(ctx, portal, sourceEvent, state)
}

// appendCodexToolOutput accumulates output deltas per tool-call and returns the full buffer.
func (cc *CodexClient) appendCodexToolOutput(state *streamingState, toolCallID, delta string) string {
	if state == nil || toolCallID == "" {
		return delta
	}
	if state.codexToolOutputBuffers == nil {
		state.codexToolOutputBuffers = make(map[string]*strings.Builder)
	}
	b := state.codexToolOutputBuffers[toolCallID]
	if b == nil {
		b = &strings.Builder{}
		state.codexToolOutputBuffers[toolCallID] = b
	}
	b.WriteString(delta)
	return b.String()
}

// handleSimpleOutputDelta is a shared handler for simple tool output delta events
// (commandExecution, fileChange, collabToolCall, plan).
func (cc *CodexClient) handleSimpleOutputDelta(
	ctx context.Context, portal *bridgev2.Portal, state *streamingState,
	params json.RawMessage, threadID, turnID, defaultToolName string,
) {
	var p struct {
		Delta  string `json:"delta"`
		ItemID string `json:"itemId"`
		Thread string `json:"threadId"`
		Turn   string `json:"turnId"`
	}
	_ = json.Unmarshal(params, &p)
	if p.Thread != threadID || p.Turn != turnID {
		return
	}
	toolCallID := strings.TrimSpace(p.ItemID)
	if toolCallID == "" {
		toolCallID = defaultToolName
	}
	buf := cc.appendCodexToolOutput(state, toolCallID, p.Delta)
	cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, toolCallID, buf, true, true)
}

// codexTurnCompletedStatus checks if a notification signals turn completion.
func codexTurnCompletedStatus(evt codexNotif, threadID, turnID string) (status string, errText string, ok bool) {
	if evt.Method != "turn/completed" {
		return "", "", false
	}
	var p struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Turn     struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
	}
	_ = json.Unmarshal(evt.Params, &p)
	if tid := strings.TrimSpace(p.ThreadID); tid != "" && tid != threadID {
		return "", "", false
	}
	if tid := strings.TrimSpace(p.TurnID); tid != "" && tid != turnID {
		return "", "", false
	}
	if tid := strings.TrimSpace(p.Turn.ID); tid != "" && tid != turnID {
		return "", "", false
	}
	status = strings.TrimSpace(p.Turn.Status)
	if status == "" {
		status = "completed"
	}
	if p.Turn.Error != nil {
		errText = strings.TrimSpace(p.Turn.Error.Message)
	}
	return status, errText, true
}

// handleNotif dispatches a single Codex notification to the appropriate handler.
func (cc *CodexClient) handleNotif(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, state *streamingState, model, threadID, turnID string, evt codexNotif) {
	switch evt.Method {
	case "error":
		cc.handleErrorNotif(ctx, portal, state, evt.Params)
	case "item/agentMessage/delta":
		cc.handleAgentMessageDelta(ctx, portal, state, evt.Params, threadID, turnID)
	case "item/reasoning/summaryTextDelta":
		cc.handleReasoningSummaryDelta(ctx, portal, state, evt.Params, threadID, turnID)
	case "item/reasoning/summaryPartAdded":
		cc.handleReasoningSummaryPartAdded(ctx, portal, state, evt.Params, threadID, turnID)
	case "item/reasoning/textDelta":
		cc.handleReasoningTextDelta(ctx, portal, state, evt.Params, threadID, turnID)
	case "item/commandExecution/outputDelta":
		cc.handleSimpleOutputDelta(ctx, portal, state, evt.Params, threadID, turnID, "commandExecution")
	case "item/fileChange/outputDelta":
		cc.handleSimpleOutputDelta(ctx, portal, state, evt.Params, threadID, turnID, "fileChange")
	case "item/mcpToolCall/outputDelta":
		cc.handleMCPToolOutputDelta(ctx, portal, state, evt.Params, threadID, turnID)
	case "item/collabToolCall/outputDelta":
		cc.handleSimpleOutputDelta(ctx, portal, state, evt.Params, threadID, turnID, "collabToolCall")
	case "turn/diff/updated":
		cc.handleTurnDiffUpdated(ctx, portal, state, evt.Params, threadID, turnID)
	case "item/plan/delta":
		cc.handleSimpleOutputDelta(ctx, portal, state, evt.Params, threadID, turnID, "plan")
	case "turn/plan/updated":
		cc.handleTurnPlanUpdated(ctx, portal, state, evt.Params, threadID, turnID)
	case "thread/tokenUsage/updated":
		cc.handleTokenUsageUpdated(ctx, portal, state, evt.Params, model, threadID, turnID)
	case "item/started":
		cc.handleItemStartedNotif(ctx, portal, state, evt.Params, threadID, turnID)
	case "item/completed":
		cc.handleItemCompletedNotif(ctx, portal, state, evt.Params, threadID, turnID)
	}
}

func (cc *CodexClient) handleErrorNotif(ctx context.Context, portal *bridgev2.Portal, state *streamingState, params json.RawMessage) {
	var p struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(params, &p)
	if msg := strings.TrimSpace(p.Error.Message); msg != "" {
		cc.uiEmitter(state).EmitUIError(ctx, portal, msg)
		cc.sendSystemNoticeOnce(ctx, portal, state, "turn:error", "Codex error: "+msg)
	}
}

// threadTurnParams is a reusable struct for notifications containing threadId+turnId fields.
type threadTurnParams struct {
	Delta  string `json:"delta"`
	ItemID string `json:"itemId"`
	Thread string `json:"threadId"`
	Turn   string `json:"turnId"`
}

func parseThreadTurnDelta(params json.RawMessage) threadTurnParams {
	var p threadTurnParams
	_ = json.Unmarshal(params, &p)
	return p
}

func (cc *CodexClient) handleAgentMessageDelta(ctx context.Context, portal *bridgev2.Portal, state *streamingState, params json.RawMessage, threadID, turnID string) {
	p := parseThreadTurnDelta(params)
	if p.Thread != threadID || p.Turn != turnID {
		return
	}
	if state.firstToken {
		state.firstToken = false
		state.firstTokenAtMs = time.Now().UnixMilli()
	}
	state.accumulated.WriteString(p.Delta)
	state.visibleAccumulated.WriteString(p.Delta)
	cc.uiEmitter(state).EmitUITextDelta(ctx, portal, p.Delta)
}

func (cc *CodexClient) handleReasoningSummaryDelta(ctx context.Context, portal *bridgev2.Portal, state *streamingState, params json.RawMessage, threadID, turnID string) {
	p := parseThreadTurnDelta(params)
	if p.Thread != threadID || p.Turn != turnID {
		return
	}
	state.codexReasoningSummarySeen = true
	if state.firstToken {
		state.firstToken = false
		state.firstTokenAtMs = time.Now().UnixMilli()
	}
	state.reasoning.WriteString(p.Delta)
	cc.uiEmitter(state).EmitUIReasoningDelta(ctx, portal, p.Delta)
}

func (cc *CodexClient) handleReasoningSummaryPartAdded(ctx context.Context, portal *bridgev2.Portal, state *streamingState, params json.RawMessage, threadID, turnID string) {
	p := parseThreadTurnDelta(params)
	if p.Thread != threadID || p.Turn != turnID {
		return
	}
	state.codexReasoningSummarySeen = true
	if state.reasoning.Len() > 0 {
		state.reasoning.WriteString("\n")
		cc.uiEmitter(state).EmitUIReasoningDelta(ctx, portal, "\n")
	}
}

func (cc *CodexClient) handleReasoningTextDelta(ctx context.Context, portal *bridgev2.Portal, state *streamingState, params json.RawMessage, threadID, turnID string) {
	p := parseThreadTurnDelta(params)
	if p.Thread != threadID || p.Turn != turnID {
		return
	}
	// Prefer summary deltas when present to avoid duplicate reasoning output.
	if state.codexReasoningSummarySeen {
		return
	}
	if state.firstToken {
		state.firstToken = false
		state.firstTokenAtMs = time.Now().UnixMilli()
	}
	state.reasoning.WriteString(p.Delta)
	cc.uiEmitter(state).EmitUIReasoningDelta(ctx, portal, p.Delta)
}

func (cc *CodexClient) handleMCPToolOutputDelta(ctx context.Context, portal *bridgev2.Portal, state *streamingState, params json.RawMessage, threadID, turnID string) {
	var p struct {
		Delta  string `json:"delta"`
		ItemID string `json:"itemId"`
		Tool   string `json:"tool"`
		Thread string `json:"threadId"`
		Turn   string `json:"turnId"`
	}
	_ = json.Unmarshal(params, &p)
	if p.Thread != threadID || p.Turn != turnID {
		return
	}
	toolCallID := strings.TrimSpace(p.ItemID)
	toolName := strings.TrimSpace(p.Tool)
	if toolName == "" {
		toolName = "mcpToolCall"
	}
	if toolCallID == "" {
		toolCallID = toolName
	}
	buf := cc.appendCodexToolOutput(state, toolCallID, p.Delta)
	cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, toolCallID, buf, true, true)
}

func (cc *CodexClient) handleTurnDiffUpdated(ctx context.Context, portal *bridgev2.Portal, state *streamingState, params json.RawMessage, threadID, turnID string) {
	var p struct {
		Thread string `json:"threadId"`
		Turn   string `json:"turnId"`
		Diff   string `json:"diff"`
	}
	_ = json.Unmarshal(params, &p)
	if p.Thread != threadID || p.Turn != turnID {
		return
	}
	state.codexLatestDiff = p.Diff
	diffToolID := fmt.Sprintf("diff-%s", turnID)
	cc.ensureUIToolInputStart(ctx, portal, state, diffToolID, "diff", true, map[string]any{"turnId": turnID})
	cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, diffToolID, p.Diff, true, true)
}

func (cc *CodexClient) handleTurnPlanUpdated(ctx context.Context, portal *bridgev2.Portal, state *streamingState, params json.RawMessage, threadID, turnID string) {
	var p struct {
		Thread      string           `json:"threadId"`
		Turn        string           `json:"turnId"`
		Explanation *string          `json:"explanation"`
		Plan        []map[string]any `json:"plan"`
	}
	_ = json.Unmarshal(params, &p)
	if p.Thread != threadID || p.Turn != turnID {
		return
	}
	toolCallID := fmt.Sprintf("turn-plan-%s", turnID)
	input := map[string]any{}
	if p.Explanation != nil && strings.TrimSpace(*p.Explanation) != "" {
		input["explanation"] = strings.TrimSpace(*p.Explanation)
	}
	cc.ensureUIToolInputStart(ctx, portal, state, toolCallID, "plan", true, input)
	cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, toolCallID, map[string]any{
		"explanation": input["explanation"],
		"plan":        p.Plan,
	}, true, true)
	cc.sendSystemNoticeOnce(ctx, portal, state, "turn:plan_updated", "Codex updated the plan.")
}

func (cc *CodexClient) handleTokenUsageUpdated(ctx context.Context, portal *bridgev2.Portal, state *streamingState, params json.RawMessage, model, threadID, turnID string) {
	var p struct {
		Thread     string `json:"threadId"`
		Turn       string `json:"turnId"`
		TokenUsage struct {
			Total struct {
				InputTokens           int64 `json:"inputTokens"`
				CachedInputTokens     int64 `json:"cachedInputTokens"`
				OutputTokens          int64 `json:"outputTokens"`
				ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
				TotalTokens           int64 `json:"totalTokens"`
			} `json:"total"`
		} `json:"tokenUsage"`
	}
	_ = json.Unmarshal(params, &p)
	if p.Thread != threadID || p.Turn != turnID {
		return
	}
	state.promptTokens = p.TokenUsage.Total.InputTokens + p.TokenUsage.Total.CachedInputTokens
	state.completionTokens = p.TokenUsage.Total.OutputTokens
	state.reasoningTokens = p.TokenUsage.Total.ReasoningOutputTokens
	state.totalTokens = p.TokenUsage.Total.TotalTokens
	cc.uiEmitter(state).EmitUIMessageMetadata(ctx, portal, cc.buildUIMessageMetadata(state, model, true, ""))
}

func (cc *CodexClient) handleItemStartedNotif(ctx context.Context, portal *bridgev2.Portal, state *streamingState, params json.RawMessage, threadID, turnID string) {
	var p struct {
		Thread string          `json:"threadId"`
		Turn   string          `json:"turnId"`
		Item   json.RawMessage `json:"item"`
	}
	_ = json.Unmarshal(params, &p)
	if p.Thread != threadID || p.Turn != turnID {
		return
	}
	cc.handleItemStarted(ctx, portal, state, p.Item)
}

func (cc *CodexClient) handleItemCompletedNotif(ctx context.Context, portal *bridgev2.Portal, state *streamingState, params json.RawMessage, threadID, turnID string) {
	var p struct {
		Thread string          `json:"threadId"`
		Turn   string          `json:"turnId"`
		Item   json.RawMessage `json:"item"`
	}
	_ = json.Unmarshal(params, &p)
	if p.Thread != threadID || p.Turn != turnID {
		return
	}
	cc.handleItemCompleted(ctx, portal, state, p.Item)
}

// handleItemStarted processes item/started notifications from Codex.
func (cc *CodexClient) handleItemStarted(ctx context.Context, portal *bridgev2.Portal, state *streamingState, raw json.RawMessage) {
	var probe struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	_ = json.Unmarshal(raw, &probe)
	itemID := strings.TrimSpace(probe.ID)

	switch probe.Type {
	case "agentMessage", "reasoning":
		// Streaming comes via dedicated delta events; nothing to do on start.
		return
	case "webSearch":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.ensureUIToolInputStart(ctx, portal, state, itemID, "webSearch", true, it)
		notice := "Codex started web search."
		if q, ok := it["query"].(string); ok && strings.TrimSpace(q) != "" {
			notice = fmt.Sprintf("Codex started web search: %s", strings.TrimSpace(q))
		}
		cc.sendSystemNoticeOnce(ctx, portal, state, "websearch:"+itemID, notice)
	case "imageView":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.ensureUIToolInputStart(ctx, portal, state, itemID, "imageView", true, it)
		cc.sendSystemNoticeOnce(ctx, portal, state, "imageview:"+itemID, "Codex viewed an image.")
	case "enteredReviewMode":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.ensureUIToolInputStart(ctx, portal, state, itemID, "review", true, it)
		cc.sendSystemNoticeOnce(ctx, portal, state, "review:entered:"+itemID, "Codex entered review mode.")
	case "exitedReviewMode":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.ensureUIToolInputStart(ctx, portal, state, itemID, "review", true, it)
		cc.sendSystemNoticeOnce(ctx, portal, state, "review:exited:"+itemID, "Codex exited review mode.")
	case "contextCompaction":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.ensureUIToolInputStart(ctx, portal, state, itemID, "contextCompaction", true, it)
		cc.sendSystemNoticeOnce(ctx, portal, state, "compaction:started:"+itemID, "Codex is compacting context...")
	default:
		// commandExecution, fileChange, mcpToolCall, collabToolCall, plan
		cc.handleGenericItemStarted(ctx, portal, state, raw, probe.Type, itemID)
	}
}

// handleGenericItemStarted handles the common case for tool-type item/started events.
func (cc *CodexClient) handleGenericItemStarted(ctx context.Context, portal *bridgev2.Portal, state *streamingState, raw json.RawMessage, itemType, itemID string) {
	var it map[string]any
	_ = json.Unmarshal(raw, &it)
	toolName := itemType
	if itemType == "mcpToolCall" {
		if tn, _ := it["tool"].(string); strings.TrimSpace(tn) != "" {
			toolName = strings.TrimSpace(tn)
		}
	}
	cc.ensureUIToolInputStart(ctx, portal, state, itemID, toolName, true, it)
}

// newProviderToolCall creates a completed provider tool call metadata entry.
func newProviderToolCall(id, name string, output map[string]any) ToolCallMetadata {
	now := time.Now().UnixMilli()
	return ToolCallMetadata{
		CallID:        id,
		ToolName:      name,
		ToolType:      string(matrixevents.ToolTypeProvider),
		Output:        output,
		Status:        string(matrixevents.ToolStatusCompleted),
		ResultStatus:  string(matrixevents.ResultStatusSuccess),
		StartedAtMs:   now,
		CompletedAtMs: now,
	}
}

// handleItemCompleted processes item/completed notifications from Codex.
func (cc *CodexClient) handleItemCompleted(ctx context.Context, portal *bridgev2.Portal, state *streamingState, raw json.RawMessage) {
	var probe struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	_ = json.Unmarshal(raw, &probe)
	itemID := strings.TrimSpace(probe.ID)

	switch probe.Type {
	case "agentMessage":
		cc.handleAgentMessageCompleted(ctx, portal, state, raw)
	case "reasoning":
		cc.handleReasoningCompleted(ctx, portal, state, raw)
	case "commandExecution", "fileChange", "mcpToolCall":
		cc.handleToolItemCompleted(ctx, portal, state, raw, itemID)
	case "collabToolCall":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, it, true, false)
		state.toolCalls = append(state.toolCalls, newProviderToolCall(itemID, "collabToolCall", it))
	case "webSearch":
		cc.handleWebSearchCompleted(ctx, portal, state, raw, itemID)
	case "imageView":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, it, true, false)
		state.toolCalls = append(state.toolCalls, newProviderToolCall(itemID, "imageView", it))
	case "plan":
		var it struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &it)
		if text := strings.TrimSpace(it.Text); text != "" {
			cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, text, true, false)
			state.toolCalls = append(state.toolCalls, newProviderToolCall(itemID, "plan", map[string]any{"text": text}))
		}
	case "enteredReviewMode":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, it, true, false)
		state.toolCalls = append(state.toolCalls, newProviderToolCall(itemID, "review", it))
	case "exitedReviewMode":
		var it struct {
			Review string `json:"review"`
		}
		_ = json.Unmarshal(raw, &it)
		if text := strings.TrimSpace(it.Review); text != "" {
			cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, text, true, false)
			state.toolCalls = append(state.toolCalls, newProviderToolCall(itemID, "review", map[string]any{"review": text}))
		}
	case "contextCompaction":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, it, true, false)
		state.toolCalls = append(state.toolCalls, newProviderToolCall(itemID, "contextCompaction", it))
		cc.sendSystemNoticeOnce(ctx, portal, state, "compaction:completed:"+itemID, "Codex finished compacting context.")
	}
}

func (cc *CodexClient) handleAgentMessageCompleted(ctx context.Context, portal *bridgev2.Portal, state *streamingState, raw json.RawMessage) {
	// If delta events were dropped, backfill once from the completed item.
	if state != nil && strings.TrimSpace(state.accumulated.String()) != "" {
		return
	}
	var it struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &it)
	if text := strings.TrimSpace(it.Text); text != "" {
		state.accumulated.WriteString(it.Text)
		state.visibleAccumulated.WriteString(it.Text)
		cc.uiEmitter(state).EmitUITextDelta(ctx, portal, it.Text)
	}
}

func (cc *CodexClient) handleReasoningCompleted(ctx context.Context, portal *bridgev2.Portal, state *streamingState, raw json.RawMessage) {
	// If reasoning deltas were dropped, backfill once from the completed item.
	if state != nil && strings.TrimSpace(state.reasoning.String()) != "" {
		return
	}
	var it struct {
		Summary []string `json:"summary"`
		Content []string `json:"content"`
	}
	_ = json.Unmarshal(raw, &it)
	var text string
	if len(it.Summary) > 0 {
		text = strings.Join(it.Summary, "\n")
	} else if len(it.Content) > 0 {
		text = strings.Join(it.Content, "\n")
	}
	if text = strings.TrimSpace(text); text != "" {
		state.reasoning.WriteString(text)
		cc.uiEmitter(state).EmitUIReasoningDelta(ctx, portal, text)
	}
}

func (cc *CodexClient) handleToolItemCompleted(ctx context.Context, portal *bridgev2.Portal, state *streamingState, raw json.RawMessage, itemID string) {
	var it map[string]any
	_ = json.Unmarshal(raw, &it)
	statusVal, _ := it["status"].(string)
	statusVal = strings.TrimSpace(statusVal)

	switch statusVal {
	case "declined":
		cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, map[string]any{
			"type":       "tool-output-denied",
			"toolCallId": itemID,
		}, true, false)
	case "failed":
		errText := extractToolErrorMessage(it)
		cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, map[string]any{
			"type":             "tool-output-error",
			"toolCallId":       itemID,
			"errorText":        errText,
			"providerExecuted": true,
		}, true, false)
	default:
		cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, it, true, false)
	}

	tc := newProviderToolCall(itemID, fmt.Sprintf("%v", it["type"]), it)
	switch statusVal {
	case "declined":
		tc.ResultStatus = string(matrixevents.ResultStatusDenied)
		tc.ErrorMessage = "Denied by user"
	case "failed":
		tc.ResultStatus = string(matrixevents.ResultStatusError)
		tc.ErrorMessage = extractToolErrorMessage(it)
	default:
		tc.ResultStatus = string(matrixevents.ResultStatusSuccess)
	}
	state.toolCalls = append(state.toolCalls, tc)
}

// extractToolErrorMessage pulls an error message from a tool item's error object.
func extractToolErrorMessage(it map[string]any) string {
	if errObj, ok := it["error"].(map[string]any); ok {
		if msg, ok := errObj["message"].(string); ok && strings.TrimSpace(msg) != "" {
			return strings.TrimSpace(msg)
		}
	}
	return "tool failed"
}

func (cc *CodexClient) handleWebSearchCompleted(ctx context.Context, portal *bridgev2.Portal, state *streamingState, raw json.RawMessage, itemID string) {
	var it map[string]any
	_ = json.Unmarshal(raw, &it)
	cc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, itemID, it, true, false)
	state.toolCalls = append(state.toolCalls, newProviderToolCall(itemID, "webSearch", it))
	// Extract web search citations and emit source-url stream events.
	if outputJSON, err := json.Marshal(it); err == nil {
		collectToolOutputCitations(state, "webSearch", string(outputJSON))
		for _, citation := range state.sourceCitations {
			cc.uiEmitter(state).EmitUISourceURL(ctx, portal, citation)
		}
	}
}
