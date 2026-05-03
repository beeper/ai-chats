package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/sdk"
)

type codexNotifFields struct {
	Delta  string `json:"delta"`
	ItemID string `json:"itemId"`
	Thread string `json:"threadId"`
	Turn   string `json:"turnId"`
}

// parseNotifFields unmarshals common fields and returns false if the notification
// does not belong to the given thread/turn pair.
func parseNotifFields(params json.RawMessage, threadID, turnID string) (codexNotifFields, bool) {
	var f codexNotifFields
	_ = json.Unmarshal(params, &f)
	return f, f.Thread == threadID && f.Turn == turnID
}

var codexSimpleOutputDeltaMethods = map[string]string{
	"item/commandExecution/outputDelta": "commandExecution",
	"item/fileChange/outputDelta":       "fileChange",
	"item/collabToolCall/outputDelta":   "collabToolCall",
	"item/plan/delta":                   "plan",
}

type toolNameExtractor func(json.RawMessage) (name string, inputKey string)

func (cc *CodexClient) handleSimpleOutputDelta(
	ctx context.Context, state *streamingState,
	params json.RawMessage, threadID, turnID, defaultToolName string,
	extractName toolNameExtractor,
) {
	f, ok := parseNotifFields(params, threadID, turnID)
	if !ok {
		return
	}
	toolName := defaultToolName
	inputMap := map[string]any{}
	if extractName != nil {
		if name, key := extractName(params); name != "" {
			toolName = name
			if key != "" {
				inputMap[key] = name
			}
		}
	}
	toolCallID := strings.TrimSpace(f.ItemID)
	if toolCallID == "" {
		toolCallID = toolName
	}
	buf := cc.appendCodexToolOutput(state, toolCallID, f.Delta)
	if state.turn != nil {
		state.turn.Writer().Tools().EnsureInputStart(ctx, toolCallID, inputMap, sdk.ToolInputOptions{
			ToolName:         toolName,
			ProviderExecuted: true,
		})
		state.turn.Writer().Tools().Output(ctx, toolCallID, buf, sdk.ToolOutputOptions{
			ProviderExecuted: true,
			Streaming:        true,
		})
	}
}

func (cc *CodexClient) handleNotif(ctx context.Context, portal *bridgev2.Portal, portalState *codexPortalState, state *streamingState, model, threadID, turnID string, evt codexNotif) {
	if defaultToolName, ok := codexSimpleOutputDeltaMethods[evt.Method]; ok {
		cc.handleSimpleOutputDelta(ctx, state, evt.Params, threadID, turnID, defaultToolName, nil)
		return
	}
	parseFields := func() (codexNotifFields, bool) {
		return parseNotifFields(evt.Params, threadID, turnID)
	}
	appendReasoningDelta := func(delta string) {
		state.recordFirstToken()
		state.reasoning.WriteString(delta)
		if state.turn != nil {
			state.turn.Writer().ReasoningDelta(ctx, delta)
		}
	}
	switch evt.Method {
	case "error":
		var p struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(evt.Params, &p)
		if strings.TrimSpace(p.Error.Message) != "" {
			if state.turn != nil {
				state.turn.Writer().Error(ctx, p.Error.Message)
			}
			cc.sendSystemNoticeOnce(ctx, portal, state, "turn:error", "Codex error: "+strings.TrimSpace(p.Error.Message))
		}
	case "item/agentMessage/delta":
		f, ok := parseFields()
		if !ok {
			return
		}
		state.recordFirstToken()
		state.accumulated.WriteString(f.Delta)
		if state.turn != nil {
			state.turn.Writer().TextDelta(ctx, f.Delta)
		}
	case "item/reasoning/summaryTextDelta":
		f, ok := parseFields()
		if !ok {
			return
		}
		state.codexReasoningSummarySeen = true
		appendReasoningDelta(f.Delta)
	case "item/reasoning/summaryPartAdded":
		if _, ok := parseFields(); !ok {
			return
		}
		state.codexReasoningSummarySeen = true
		if state.reasoning.Len() > 0 {
			state.reasoning.WriteString("\n")
			if state.turn != nil {
				state.turn.Writer().ReasoningDelta(ctx, "\n")
			}
		}
	case "item/reasoning/textDelta":
		f, ok := parseFields()
		if !ok || state.codexReasoningSummarySeen {
			// Prefer summary deltas when present to avoid duplicate reasoning output.
			return
		}
		appendReasoningDelta(f.Delta)
	case "item/mcpToolCall/outputDelta":
		cc.handleSimpleOutputDelta(ctx, state, evt.Params, threadID, turnID, "mcpToolCall", func(raw json.RawMessage) (string, string) {
			var extra struct {
				Tool string `json:"tool"`
			}
			_ = json.Unmarshal(raw, &extra)
			if name := strings.TrimSpace(extra.Tool); name != "" {
				return name, "tool"
			}
			return "", ""
		})
	case "model/rerouted":
		f, ok := parseFields()
		if !ok {
			return
		}
		var p struct {
			ToModel string `json:"toModel"`
		}
		_ = json.Unmarshal(evt.Params, &p)
		nextModel := strings.TrimSpace(p.ToModel)
		if nextModel == "" {
			return
		}
		state.currentModel = nextModel
		if state.turn != nil {
			state.turn.Writer().MessageMetadata(ctx, cc.buildUIMessageMetadata(state, nextModel, true, ""))
		}
		cc.activeMu.Lock()
		if active := cc.activeTurns[codexTurnKey(f.Thread, f.Turn)]; active != nil {
			active.model = nextModel
		}
		cc.activeMu.Unlock()
	case "turn/diff/updated":
		if _, ok := parseFields(); !ok {
			return
		}
		var diffPayload struct {
			Diff string `json:"diff"`
		}
		_ = json.Unmarshal(evt.Params, &diffPayload)
		state.codexLatestDiff = diffPayload.Diff
		emitDiffToolOutput(ctx, state, fmt.Sprintf("diff-%s", turnID), turnID, diffPayload.Diff, true)
	case "turn/plan/updated":
		if _, ok := parseFields(); !ok {
			return
		}
		var p struct {
			Explanation *string          `json:"explanation"`
			Plan        []map[string]any `json:"plan"`
		}
		_ = json.Unmarshal(evt.Params, &p)
		toolCallID := fmt.Sprintf("turn-plan-%s", turnID)
		input := map[string]any{}
		if p.Explanation != nil && strings.TrimSpace(*p.Explanation) != "" {
			input["explanation"] = strings.TrimSpace(*p.Explanation)
		}
		if state.turn != nil {
			state.turn.Writer().Tools().EnsureInputStart(ctx, toolCallID, input, sdk.ToolInputOptions{
				ToolName:         "plan",
				ProviderExecuted: true,
			})
			state.turn.Writer().Tools().Output(ctx, toolCallID, map[string]any{
				"explanation": input["explanation"],
				"plan":        p.Plan,
			}, sdk.ToolOutputOptions{
				ProviderExecuted: true,
				Streaming:        true,
			})
		}
		cc.sendSystemNoticeOnce(ctx, portal, state, "turn:plan_updated", "Codex updated the plan.")
	case "thread/tokenUsage/updated":
		if _, ok := parseFields(); !ok {
			return
		}
		var p struct {
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
		_ = json.Unmarshal(evt.Params, &p)
		state.promptTokens = p.TokenUsage.Total.InputTokens + p.TokenUsage.Total.CachedInputTokens
		state.completionTokens = p.TokenUsage.Total.OutputTokens
		state.reasoningTokens = p.TokenUsage.Total.ReasoningOutputTokens
		state.totalTokens = p.TokenUsage.Total.TotalTokens
		if state.turn != nil {
			state.turn.Writer().MessageMetadata(ctx, cc.buildUIMessageMetadata(state, codexStateModel(state, model), true, ""))
		}
	case "item/started", "item/completed":
		if _, ok := parseFields(); !ok {
			return
		}
		var p struct {
			Item json.RawMessage `json:"item"`
		}
		_ = json.Unmarshal(evt.Params, &p)
		if evt.Method == "item/started" {
			cc.handleItemStarted(ctx, portal, state, p.Item)
		} else {
			cc.handleItemCompleted(ctx, portal, state, p.Item)
		}
	}
}

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
	// Each ID field, when present, must match the expected value.
	for _, pair := range [][2]string{
		{strings.TrimSpace(p.ThreadID), threadID},
		{strings.TrimSpace(p.TurnID), turnID},
	} {
		if pair[0] != "" && pair[0] != pair[1] {
			return "", "", false
		}
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
