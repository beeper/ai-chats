package opencode

import (
	"strings"

	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/bridges/opencode/api"
	"github.com/beeper/agentremote/pkg/matrixevents"
	"github.com/beeper/agentremote/pkg/shared/streamui"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

type canonicalBackfillSnapshot struct {
	body string
	ui   map[string]any
	meta *MessageMetadata
}

func buildCanonicalAssistantBackfill(msg api.MessageWithParts, agentID string) canonicalBackfillSnapshot {
	turnID := opencodeMessageStreamTurnID(msg.Info.SessionID, msg.Info.ID)
	if turnID == "" {
		turnID = "opencode-msg-" + strings.TrimSpace(msg.Info.ID)
	}
	state := streamui.UIState{TurnID: turnID}
	startMeta := buildTurnStartMetadata(&msg, agentID)
	state.InitMaps()
	opencodeReplayStart(&state, startMeta)

	var visible strings.Builder

	for _, part := range msg.Parts {
		fillPartIDs(&part, msg.Info.ID, msg.Info.SessionID)
		appendCanonicalAssistantPart(&state, &visible, part)
	}

	finishReason := strings.TrimSpace(msg.Info.Finish)
	if finishReason == "" {
		finishReason = "stop"
	}
	finishMeta := buildTurnFinishMetadata(&msg, agentID, finishReason)
	opencodeReplayFinish(&state, finishReason, finishMeta)

	uiMessage := streamui.SnapshotUIMessage(&state)
	body := strings.TrimSpace(visible.String())
	if body == "" {
		body = "..."
	}
	promptTokens, completionTokens, reasoningTokens := backfillTokenCounts(msg)
	return canonicalBackfillSnapshot{
		body: body,
		ui:   uiMessage,
		meta: buildMessageMetadataFromParams(MessageMetadataParams{
			Role:             stringutil.FirstNonEmpty(strings.TrimSpace(msg.Info.Role), "assistant"),
			Body:             body,
			FinishReason:     stringutil.FirstNonEmpty(strings.TrimSpace(msg.Info.Finish), finishReason),
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			ReasoningTokens:  reasoningTokens,
			TurnID:           turnID,
			AgentID:          strings.TrimSpace(agentID),
			UIMessage:        uiMessage,
			StartedAtMs:      int64(msg.Info.Time.Created),
			CompletedAtMs:    int64(msg.Info.Time.Completed),
			SessionID:        strings.TrimSpace(msg.Info.SessionID),
			MessageID:        strings.TrimSpace(msg.Info.ID),
			ParentMessageID:  strings.TrimSpace(msg.Info.ParentID),
			Agent:            strings.TrimSpace(msg.Info.Agent),
			ModelID:          strings.TrimSpace(msg.Info.ModelID),
			ProviderID:       strings.TrimSpace(msg.Info.ProviderID),
			Mode:             strings.TrimSpace(msg.Info.Mode),
			Cost:             backfillCost(msg),
			TotalTokens:      backfillTotalTokens(msg),
		}),
	}
}

func appendCanonicalAssistantPart(state *streamui.UIState, visible *strings.Builder, part api.Part) {
	switch part.Type {
	case "text":
		if part.ID == "" || part.Text == "" {
			return
		}
		opencodeReplayText(state, visible, opencodePartStreamID(part, "text"), part.Text)
	case "reasoning":
		if part.ID == "" || part.Text == "" {
			return
		}
		opencodeReplayReasoning(state, opencodePartStreamID(part, "reasoning"), part.Text)
	case "tool":
		appendCanonicalToolPart(state, part)
		if part.State != nil {
			for _, attachment := range part.State.Attachments {
				fillPartIDs(&attachment, part.MessageID, part.SessionID)
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

func appendCanonicalToolPart(state *streamui.UIState, part api.Part) {
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
				"providerExecuted": false,
			})
			streamui.ApplyChunk(state, map[string]any{
				"type":             "tool-input-delta",
				"toolCallId":       toolCallID,
				"inputTextDelta":   strings.TrimSpace(part.State.Raw),
				"providerExecuted": false,
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
				"errorText":        strings.TrimSpace(part.State.Error),
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

func appendCanonicalArtifactParts(state *streamui.UIState, part api.Part) {
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
		streamui.ApplyChunk(state, map[string]any{
			"type":      "source-document",
			"sourceId":  "opencode-doc-" + part.ID,
			"title":     title,
			"filename":  title,
			"mediaType": mediaType,
		})
	}
}

func opencodeReplayStart(state *streamui.UIState, metadata map[string]any) {
	part := map[string]any{
		"type":      "start",
		"messageId": state.TurnID,
	}
	if len(metadata) > 0 {
		part["messageMetadata"] = metadata
	}
	streamui.ApplyChunk(state, part)
}

func opencodeReplayFinish(state *streamui.UIState, finishReason string, metadata map[string]any) {
	finishReason = strings.TrimSpace(finishReason)
	if finishReason == "" {
		finishReason = "stop"
	}
	part := map[string]any{
		"type":         "finish",
		"finishReason": finishReason,
	}
	if len(metadata) > 0 {
		part["messageMetadata"] = metadata
	}
	streamui.ApplyChunk(state, part)
}

func opencodeReplayText(state *streamui.UIState, visible *strings.Builder, partID, text string) {
	partID = strings.TrimSpace(partID)
	text = strings.TrimSpace(text)
	if partID == "" || text == "" {
		return
	}
	streamui.ApplyChunk(state, map[string]any{"type": "text-start", "id": partID})
	streamui.ApplyChunk(state, map[string]any{"type": "text-delta", "id": partID, "delta": text})
	streamui.ApplyChunk(state, map[string]any{"type": "text-end", "id": partID})
	visible.WriteString(text)
}

func opencodeReplayReasoning(state *streamui.UIState, partID, text string) {
	partID = strings.TrimSpace(partID)
	text = strings.TrimSpace(text)
	if partID == "" || text == "" {
		return
	}
	streamui.ApplyChunk(state, map[string]any{"type": "reasoning-start", "id": partID})
	streamui.ApplyChunk(state, map[string]any{"type": "reasoning-delta", "id": partID, "delta": text})
	streamui.ApplyChunk(state, map[string]any{"type": "reasoning-end", "id": partID})
}

func canonicalDataPart(part api.Part) map[string]any {
	if strings.TrimSpace(part.ID) == "" {
		return nil
	}
	return BuildDataPartMap(part)
}

func backfillCost(msg api.MessageWithParts) float64 {
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

func backfillTokenCounts(msg api.MessageWithParts) (prompt, completion, reasoning int64) {
	prompt = backfillTokenValue(msg, func(tokens api.TokenUsage) int64 {
		return int64(tokens.Input)
	})
	completion = backfillTokenValue(msg, func(tokens api.TokenUsage) int64 {
		return int64(tokens.Output)
	})
	reasoning = backfillTokenValue(msg, func(tokens api.TokenUsage) int64 {
		return int64(tokens.Reasoning)
	})
	return prompt, completion, reasoning
}

func backfillTokenValue(msg api.MessageWithParts, pick func(api.TokenUsage) int64) int64 {
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

func backfillTotalTokens(msg api.MessageWithParts) int64 {
	prompt, completion, reasoning := backfillTokenCounts(msg)
	total := prompt + completion + reasoning
	if msg.Info.Tokens != nil && msg.Info.Tokens.Cache != nil {
		total += int64(msg.Info.Tokens.Cache.Read + msg.Info.Tokens.Cache.Write)
		return total
	}
	for _, part := range msg.Parts {
		if part.Tokens != nil && part.Tokens.Cache != nil {
			total += int64(part.Tokens.Cache.Read + part.Tokens.Cache.Write)
		}
	}
	return total
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
