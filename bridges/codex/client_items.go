package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/matrixevents"
	"github.com/beeper/agentremote/pkg/shared/citations"
	"github.com/beeper/agentremote/sdk"
)

func (cc *CodexClient) handleItemStarted(ctx context.Context, portal *bridgev2.Portal, state *streamingState, raw json.RawMessage) {
	var probe struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	_ = json.Unmarshal(raw, &probe)
	itemID := strings.TrimSpace(probe.ID)

	// Streaming for these types comes via dedicated delta events.
	if probe.Type == "agentMessage" || probe.Type == "reasoning" {
		return
	}

	// All remaining item types share the same unmarshal + ensureUIToolInputStart pattern.
	var it map[string]any
	_ = json.Unmarshal(raw, &it)

	toolName := probe.Type
	switch probe.Type {
	case "mcpToolCall":
		if name, _ := it["tool"].(string); strings.TrimSpace(name) != "" {
			toolName = name
		}
	case "enteredReviewMode", "exitedReviewMode":
		toolName = "review"
	}

	if state.turn != nil {
		state.turn.Writer().Tools().EnsureInputStart(ctx, itemID, it, sdk.ToolInputOptions{
			ToolName:         toolName,
			ProviderExecuted: true,
		})
	}

	// Type-specific side effects (system notices).
	switch probe.Type {
	case "webSearch":
		notice := "Codex started web search."
		if q, _ := it["query"].(string); strings.TrimSpace(q) != "" {
			notice = fmt.Sprintf("Codex started web search: %s", strings.TrimSpace(q))
		}
		cc.sendSystemNoticeOnce(ctx, portal, state, "websearch:"+itemID, notice)
	case "imageView":
		cc.sendSystemNoticeOnce(ctx, portal, state, "imageview:"+itemID, "Codex viewed an image.")
	case "enteredReviewMode":
		cc.sendSystemNoticeOnce(ctx, portal, state, "review:entered:"+itemID, "Codex entered review mode.")
	case "exitedReviewMode":
		cc.sendSystemNoticeOnce(ctx, portal, state, "review:exited:"+itemID, "Codex exited review mode.")
	case "contextCompaction":
		cc.sendSystemNoticeOnce(ctx, portal, state, "compaction:started:"+itemID, "Codex is compacting context…")
	}
}

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

func (cc *CodexClient) emitNewArtifacts(ctx context.Context, portal *bridgev2.Portal, state *streamingState, docs []citations.SourceDocument, files []citations.GeneratedFilePart) {
	for _, document := range docs {
		if state.turn != nil {
			state.turn.Writer().SourceDocument(ctx, document)
		}
	}
	for _, file := range files {
		if state.turn != nil {
			state.turn.Writer().File(ctx, file.URL, file.MediaType)
		}
	}
}

func (cc *CodexClient) handleItemCompleted(ctx context.Context, portal *bridgev2.Portal, state *streamingState, raw json.RawMessage) {
	var probe struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	_ = json.Unmarshal(raw, &probe)
	itemID := strings.TrimSpace(probe.ID)
	switch probe.Type {
	case "agentMessage":
		// If delta events were dropped, backfill once from the completed item.
		if state != nil && strings.TrimSpace(state.accumulated.String()) != "" {
			return
		}
		var it struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &it)
		if strings.TrimSpace(it.Text) == "" {
			return
		}
		state.accumulated.WriteString(it.Text)
		if state.turn != nil {
			state.turn.Writer().TextDelta(ctx, it.Text)
		}
		return
	case "reasoning":
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
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		state.reasoning.WriteString(text)
		if state.turn != nil {
			state.turn.Writer().ReasoningDelta(ctx, text)
		}
		return
	case "commandExecution", "fileChange", "mcpToolCall":
		var it map[string]any
		_ = json.Unmarshal(raw, &it)
		statusVal, _ := it["status"].(string)
		statusVal = strings.TrimSpace(statusVal)
		errText := extractItemErrorMessage(it)
		switch statusVal {
		case "declined":
			if state.turn != nil {
				state.turn.Writer().Tools().Denied(ctx, itemID)
			}
		case "failed":
			if state.turn != nil {
				state.turn.Writer().Tools().OutputError(ctx, itemID, errText, true)
			}
		default:
			if state.turn != nil {
				state.turn.Writer().Tools().Output(ctx, itemID, it, sdk.ToolOutputOptions{
					ProviderExecuted: true,
				})
			}
		}
		newDocs, newFiles := collectToolOutputArtifacts(state, it)
		cc.emitNewArtifacts(ctx, portal, state, newDocs, newFiles)

		tc := newProviderToolCall(itemID, fmt.Sprintf("%v", it["type"]), it)
		switch statusVal {
		case "declined":
			tc.ResultStatus = string(matrixevents.ResultStatusDenied)
			tc.ErrorMessage = "Denied by user"
		case "failed":
			tc.ResultStatus = string(matrixevents.ResultStatusError)
			tc.ErrorMessage = errText
		default:
			tc.ResultStatus = string(matrixevents.ResultStatusSuccess)
		}
		state.toolCalls = append(state.toolCalls, tc)
	case "collabToolCall":
		cc.emitProviderJSONToolOutput(ctx, portal, state, itemID, "collabToolCall", raw, providerJSONToolOutputOptions{collectArtifacts: true})
	case "webSearch":
		cc.emitProviderJSONToolOutput(ctx, portal, state, itemID, "webSearch", raw, providerJSONToolOutputOptions{
			collectArtifacts:        true,
			collectCitations:        true,
			appendBeforeSideEffects: true,
		})
	case "imageView":
		cc.emitProviderJSONToolOutput(ctx, portal, state, itemID, "imageView", raw, providerJSONToolOutputOptions{collectArtifacts: true})
	case "plan":
		var it struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &it)
		if !cc.emitTrimmedProviderToolTextOutput(ctx, portal, state, itemID, "plan", "text", it.Text) {
			return
		}
	case "enteredReviewMode":
		cc.emitProviderJSONToolOutput(ctx, portal, state, itemID, "review", raw, providerJSONToolOutputOptions{})
	case "exitedReviewMode":
		var it struct {
			Review string `json:"review"`
		}
		_ = json.Unmarshal(raw, &it)
		if !cc.emitTrimmedProviderToolTextOutput(ctx, portal, state, itemID, "review", "review", it.Review) {
			return
		}
	case "contextCompaction":
		cc.emitProviderJSONToolOutput(ctx, portal, state, itemID, "contextCompaction", raw, providerJSONToolOutputOptions{})
		cc.sendSystemNoticeOnce(ctx, portal, state, "compaction:completed:"+itemID, "Codex finished compacting context.")
	}
}

type providerJSONToolOutputOptions struct {
	collectArtifacts        bool
	collectCitations        bool
	appendBeforeSideEffects bool
}

func extractItemErrorMessage(it map[string]any) string {
	if errObj, ok := it["error"].(map[string]any); ok {
		if msg, ok := errObj["message"].(string); ok && strings.TrimSpace(msg) != "" {
			return strings.TrimSpace(msg)
		}
	}
	return "tool failed"
}

func (cc *CodexClient) emitProviderJSONToolOutput(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	itemID string,
	toolName string,
	raw []byte,
	opts providerJSONToolOutputOptions,
) {
	var it map[string]any
	_ = json.Unmarshal(raw, &it)
	if state.turn != nil {
		state.turn.Writer().Tools().Output(ctx, itemID, it, sdk.ToolOutputOptions{
			ProviderExecuted: true,
		})
	}
	appendToolCall := func() {
		state.toolCalls = append(state.toolCalls, newProviderToolCall(itemID, toolName, it))
	}
	if opts.appendBeforeSideEffects {
		appendToolCall()
	}
	if opts.collectCitations {
		if outputJSON, err := json.Marshal(it); err == nil {
			collectToolOutputCitations(state, toolName, string(outputJSON))
			for _, citation := range state.sourceCitations {
				if state.turn != nil {
					state.turn.Writer().SourceURL(ctx, citation)
				}
			}
		}
	}
	if opts.collectArtifacts {
		newDocs, newFiles := collectToolOutputArtifacts(state, it)
		cc.emitNewArtifacts(ctx, portal, state, newDocs, newFiles)
	}
	if !opts.appendBeforeSideEffects {
		appendToolCall()
	}
}

func (cc *CodexClient) emitTrimmedProviderToolTextOutput(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	itemID string,
	toolName string,
	field string,
	value string,
) bool {
	text := strings.TrimSpace(value)
	if text == "" {
		return false
	}
	if state.turn != nil {
		state.turn.Writer().Tools().Output(ctx, itemID, text, sdk.ToolOutputOptions{
			ProviderExecuted: true,
		})
	}
	state.toolCalls = append(state.toolCalls, newProviderToolCall(itemID, toolName, map[string]any{field: text}))
	return true
}
