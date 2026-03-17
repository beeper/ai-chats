package opencode

import (
	"strings"

	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/bridges/opencode/api"
	"github.com/beeper/agentremote/pkg/matrixevents"
	"github.com/beeper/agentremote/pkg/shared/citations"
	"github.com/beeper/agentremote/pkg/shared/streamui"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	bridgesdk "github.com/beeper/agentremote/sdk"
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
	replayer := bridgesdk.NewUIStateReplayer(&state)
	startMeta := buildTurnStartMetadata(&msg, agentID)
	state.InitMaps()
	replayer.Start(startMeta)

	var visible strings.Builder

	for _, part := range msg.Parts {
		fillPartIDs(&part, msg.Info.ID, msg.Info.SessionID)
		appendCanonicalAssistantPart(&state, replayer, &visible, part)
	}

	finishReason := strings.TrimSpace(msg.Info.Finish)
	if finishReason == "" {
		finishReason = "stop"
	}
	finishMeta := buildTurnFinishMetadata(&msg, agentID, finishReason)
	replayer.Finish(finishReason, finishMeta)

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

func appendCanonicalAssistantPart(state *streamui.UIState, replayer bridgesdk.UIStateReplayer, visible *strings.Builder, part api.Part) {
	switch part.Type {
	case "text":
		if part.ID == "" || part.Text == "" {
			return
		}
		partID := opencodePartStreamID(part, "text")
		replayer.Text(partID, part.Text)
		visible.WriteString(strings.TrimSpace(part.Text))
	case "reasoning":
		if part.ID == "" || part.Text == "" {
			return
		}
		replayer.Reasoning(opencodePartStreamID(part, "reasoning"), part.Text)
	case "tool":
		appendCanonicalToolPart(replayer, part)
		if part.State != nil {
			for _, attachment := range part.State.Attachments {
				fillPartIDs(&attachment, part.MessageID, part.SessionID)
				appendCanonicalAssistantPart(state, replayer, visible, attachment)
			}
		}
	case "file":
		appendCanonicalArtifactParts(replayer, part)
	case "step-start":
		replayer.StepStart()
	case "step-finish":
		replayer.StepFinish()
		if data := canonicalDataPart(part); data != nil {
			replayer.DataPart(data)
		}
	case "patch", "snapshot", "agent", "subtask", "retry", "compaction":
		if data := canonicalDataPart(part); data != nil {
			replayer.DataPart(data)
		}
	}
}

func appendCanonicalToolPart(replayer bridgesdk.UIStateReplayer, part api.Part) {
	toolCallID := opencodeToolCallID(part)
	if toolCallID == "" {
		return
	}
	toolName := opencodeToolName(part)
	if part.State != nil {
		if len(part.State.Input) > 0 {
			replayer.ToolInput(toolCallID, toolName, part.State.Input, false)
		} else if strings.TrimSpace(part.State.Raw) != "" {
			replayer.ToolInputText(toolCallID, toolName, strings.TrimSpace(part.State.Raw), false)
		}
		switch strings.TrimSpace(part.State.Status) {
		case "completed":
			if part.State.Output != "" {
				replayer.ToolOutput(toolCallID, part.State.Output, false)
			}
		case "error":
			replayer.ToolOutputError(toolCallID, strings.TrimSpace(part.State.Error), false)
		case "denied", "rejected":
			replayer.ToolOutputDenied(toolCallID)
		}
	}
}

func appendCanonicalArtifactParts(replayer bridgesdk.UIStateReplayer, part api.Part) {
	sourceURL := strings.TrimSpace(part.URL)
	title := strings.TrimSpace(part.Filename)
	if title == "" {
		title = strings.TrimSpace(part.Name)
	}
	mediaType := strings.TrimSpace(part.Mime)
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	replayer.Artifact(
		"opencode-source-"+part.ID,
		citations.SourceCitation{URL: sourceURL, Title: title},
		citations.SourceDocument{
			ID:        "opencode-doc-" + part.ID,
			Title:     title,
			Filename:  title,
			MediaType: mediaType,
		},
		mediaType,
	)
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
