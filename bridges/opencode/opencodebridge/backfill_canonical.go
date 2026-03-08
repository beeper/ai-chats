package opencodebridge

import (
	"strings"

	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/bridges/opencode/opencode"
	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
	"github.com/beeper/ai-bridge/pkg/matrixevents"
	"github.com/beeper/ai-bridge/pkg/shared/streamui"
	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

type canonicalBackfillSnapshot struct {
	body string
	ui   map[string]any
	meta *MessageMetadata
}

func buildCanonicalAssistantBackfill(msg opencode.MessageWithParts, agentID string) canonicalBackfillSnapshot {
	turnID := opencodeMessageStreamTurnID(msg.Info.SessionID, msg.Info.ID)
	if turnID == "" {
		turnID = "opencode-msg-" + strings.TrimSpace(msg.Info.ID)
	}
	state := streamui.UIState{TurnID: turnID}
	startMeta := buildTurnStartMetadata(&msg, agentID)
	streamui.ApplyChunk(&state, map[string]any{
		"type":            "start",
		"messageId":       turnID,
		"messageMetadata": startMeta,
	})

	var visible strings.Builder
	for _, part := range msg.Parts {
		if part.MessageID == "" {
			part.MessageID = msg.Info.ID
		}
		if part.SessionID == "" {
			part.SessionID = msg.Info.SessionID
		}
		appendCanonicalAssistantPart(&state, &visible, part)
	}

	finishReason := strings.TrimSpace(msg.Info.Finish)
	if finishReason == "" {
		finishReason = "stop"
	}
	finishMeta := buildTurnFinishMetadata(&msg, agentID, finishReason)
	streamui.ApplyChunk(&state, map[string]any{
		"type":            "finish",
		"finishReason":    finishReason,
		"messageMetadata": finishMeta,
	})

	uiMessage := streamui.SnapshotCanonicalUIMessage(&state)
	body := strings.TrimSpace(visible.String())
	if body == "" {
		body = "..."
	}
	return canonicalBackfillSnapshot{
		body: body,
		ui:   uiMessage,
		meta: &MessageMetadata{
			BaseMessageMetadata: bridgeadapter.BaseMessageMetadata{
				Role:               stringutil.FirstNonEmpty(strings.TrimSpace(msg.Info.Role), "assistant"),
				Body:               body,
				FinishReason:       stringutil.FirstNonEmpty(strings.TrimSpace(msg.Info.Finish), finishReason),
				PromptTokens:       backfillPromptTokens(msg),
				CompletionTokens:   backfillCompletionTokens(msg),
				ReasoningTokens:    backfillReasoningTokens(msg),
				TurnID:             turnID,
				AgentID:            strings.TrimSpace(agentID),
				CanonicalSchema:    "ai-sdk-ui-message-v1",
				CanonicalUIMessage: uiMessage,
				StartedAtMs:        int64(msg.Info.Time.Created),
				CompletedAtMs:      int64(msg.Info.Time.Completed),
				ThinkingContent:    CanonicalReasoningText(uiMessage),
				ToolCalls:          CanonicalToolCalls(uiMessage),
				GeneratedFiles:     CanonicalGeneratedFiles(uiMessage),
			},
			SessionID:       strings.TrimSpace(msg.Info.SessionID),
			MessageID:       strings.TrimSpace(msg.Info.ID),
			ParentMessageID: strings.TrimSpace(msg.Info.ParentID),
			Agent:           strings.TrimSpace(msg.Info.Agent),
			ModelID:         strings.TrimSpace(msg.Info.ModelID),
			ProviderID:      strings.TrimSpace(msg.Info.ProviderID),
			Mode:            strings.TrimSpace(msg.Info.Mode),
			Cost:            backfillCost(msg),
			TotalTokens:     backfillTotalTokens(msg),
		},
	}
}

func appendCanonicalAssistantPart(state *streamui.UIState, visible *strings.Builder, part opencode.Part) {
	switch part.Type {
	case "text":
		if part.ID == "" || part.Text == "" {
			return
		}
		partID := opencodePartStreamID(part, "text")
		streamui.ApplyChunk(state, map[string]any{"type": "text-start", "id": partID})
		streamui.ApplyChunk(state, map[string]any{"type": "text-delta", "id": partID, "delta": part.Text})
		streamui.ApplyChunk(state, map[string]any{"type": "text-end", "id": partID})
		visible.WriteString(part.Text)
	case "reasoning":
		if part.ID == "" || part.Text == "" {
			return
		}
		partID := opencodePartStreamID(part, "reasoning")
		streamui.ApplyChunk(state, map[string]any{"type": "reasoning-start", "id": partID})
		streamui.ApplyChunk(state, map[string]any{"type": "reasoning-delta", "id": partID, "delta": part.Text})
		streamui.ApplyChunk(state, map[string]any{"type": "reasoning-end", "id": partID})
	case "tool":
		appendCanonicalToolPart(state, part)
		if part.State != nil {
			for _, attachment := range part.State.Attachments {
				if attachment.MessageID == "" {
					attachment.MessageID = part.MessageID
				}
				if attachment.SessionID == "" {
					attachment.SessionID = part.SessionID
				}
				appendCanonicalAssistantPart(state, visible, attachment)
			}
		}
	case "file":
		appendCanonicalArtifactParts(state, part)
	case "step-start":
		streamui.ApplyChunk(state, map[string]any{"type": "start-step"})
	case "step-finish":
		streamui.ApplyChunk(state, map[string]any{"type": "finish-step"})
		if data := canonicalDataPart(part); data != nil {
			streamui.ApplyChunk(state, data)
		}
	case "patch", "snapshot", "agent", "subtask", "retry", "compaction":
		if data := canonicalDataPart(part); data != nil {
			streamui.ApplyChunk(state, data)
		}
	}
}

func appendCanonicalToolPart(state *streamui.UIState, part opencode.Part) {
	toolCallID := opencodeToolCallID(part)
	if toolCallID == "" {
		return
	}
	toolName := opencodeToolName(part)
	if part.State != nil {
		if len(part.State.Input) > 0 {
			streamui.ApplyChunk(state, map[string]any{
				"type":             "tool-input-available",
				"toolCallId":       toolCallID,
				"toolName":         toolName,
				"input":            part.State.Input,
				"providerExecuted": false,
			})
		} else if strings.TrimSpace(part.State.Raw) != "" {
			streamui.ApplyChunk(state, map[string]any{
				"type":             "tool-input-start",
				"toolCallId":       toolCallID,
				"toolName":         toolName,
				"title":            toolDisplayTitle(toolName),
				"providerExecuted": false,
			})
			streamui.ApplyChunk(state, map[string]any{
				"type":           "tool-input-delta",
				"toolCallId":     toolCallID,
				"inputTextDelta": part.State.Raw,
			})
		}
		switch strings.TrimSpace(part.State.Status) {
		case "completed":
			if part.State.Output != "" {
				streamui.ApplyChunk(state, map[string]any{
					"type":             "tool-output-available",
					"toolCallId":       toolCallID,
					"output":           part.State.Output,
					"providerExecuted": false,
				})
			}
		case "error":
			streamui.ApplyChunk(state, map[string]any{
				"type":             "tool-output-error",
				"toolCallId":       toolCallID,
				"errorText":        part.State.Error,
				"providerExecuted": false,
			})
		case "denied", "rejected":
			streamui.ApplyChunk(state, map[string]any{
				"type":       "tool-output-denied",
				"toolCallId": toolCallID,
			})
		}
	}
}

func appendCanonicalArtifactParts(state *streamui.UIState, part opencode.Part) {
	sourceURL := strings.TrimSpace(part.URL)
	title := strings.TrimSpace(part.Filename)
	if title == "" {
		title = strings.TrimSpace(part.Name)
	}
	mediaType := strings.TrimSpace(part.Mime)
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	if sourceURL != "" {
		streamui.ApplyChunk(state, map[string]any{
			"type":      "file",
			"url":       sourceURL,
			"mediaType": mediaType,
			"filename":  strings.TrimSpace(part.Filename),
		})
		streamui.ApplyChunk(state, map[string]any{
			"type":     "source-url",
			"sourceId": "opencode-source-" + part.ID,
			"url":      sourceURL,
			"title":    title,
		})
	}
	if title != "" {
		filename := strings.TrimSpace(part.Filename)
		if filename == "" {
			filename = title
		}
		streamui.ApplyChunk(state, map[string]any{
			"type":      "source-document",
			"sourceId":  "opencode-doc-" + part.ID,
			"title":     title,
			"filename":  filename,
			"mediaType": mediaType,
		})
	}
}

func canonicalDataPart(part opencode.Part) map[string]any {
	if strings.TrimSpace(part.ID) == "" {
		return nil
	}
	data := BuildDataPartMap(part)
	if data == nil {
		return nil
	}
	return data
}

func backfillCost(msg opencode.MessageWithParts) float64 {
	if msg.Info.Cost != 0 {
		return msg.Info.Cost
	}
	for _, part := range msg.Parts {
		if part.Type == "step-finish" && part.Cost != 0 {
			return part.Cost
		}
	}
	return 0
}

func backfillPromptTokens(msg opencode.MessageWithParts) int64 {
	return backfillTokenValue(msg, func(tokens opencode.TokenUsage) int64 {
		return int64(tokens.Input)
	})
}

func backfillCompletionTokens(msg opencode.MessageWithParts) int64 {
	return backfillTokenValue(msg, func(tokens opencode.TokenUsage) int64 {
		return int64(tokens.Output)
	})
}

func backfillReasoningTokens(msg opencode.MessageWithParts) int64 {
	return backfillTokenValue(msg, func(tokens opencode.TokenUsage) int64 {
		return int64(tokens.Reasoning)
	})
}

func backfillTokenValue(msg opencode.MessageWithParts, pick func(opencode.TokenUsage) int64) int64 {
	if msg.Info.Tokens != nil {
		return pick(*msg.Info.Tokens)
	}
	for _, part := range msg.Parts {
		if part.Type == "step-finish" && part.Tokens != nil {
			return pick(*part.Tokens)
		}
	}
	return 0
}

func backfillTotalTokens(msg opencode.MessageWithParts) int64 {
	return backfillPromptTokens(msg) + backfillCompletionTokens(msg) + backfillReasoningTokens(msg)
}

func buildCanonicalBackfillPart(snapshot canonicalBackfillSnapshot) *event.MessageEventContent {
	return &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    snapshot.body,
	}
}

func canonicalBackfillExtra(snapshot canonicalBackfillSnapshot) map[string]any {
	return map[string]any{
		matrixevents.BeeperAIKey: snapshot.ui,
		"m.mentions":             map[string]any{},
	}
}
