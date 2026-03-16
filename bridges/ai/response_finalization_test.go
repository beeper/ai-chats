package ai

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/shared/citations"
	"github.com/beeper/agentremote/pkg/shared/streamui"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

func testStreamingState(turnID string) *streamingState {
	conv := bridgesdk.NewConversation(context.Background(), nil, nil, bridgev2.EventSender{}, nil, nil)
	turn := conv.StartTurn(context.Background(), nil, nil)
	turn.SetID(turnID)
	return &streamingState{
		turn: turn,
	}
}

func TestBuildFinalEditUIMessage_IncludesSourceAndFileParts(t *testing.T) {
	oc := &AIClient{}
	state := testStreamingState("turn-1")
	state.sourceCitations = []citations.SourceCitation{{
		URL:      "https://example.com",
		Title:    "Example",
		SiteName: "Example Site",
	}}
	state.sourceDocuments = []citations.SourceDocument{{
		ID:        "doc-1",
		Title:     "Doc",
		Filename:  "doc.txt",
		MediaType: "text/plain",
	}}
	state.generatedFiles = []citations.GeneratedFilePart{{
		URL:       "mxc://example/file",
		MediaType: "image/png",
	}}
	state.accumulated.WriteString("hello")
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{"type": "start", "messageId": "turn-1"})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{"type": "text-start", "id": "text-1"})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{"type": "text-delta", "id": "text-1", "delta": "hello"})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{"type": "text-end", "id": "text-1"})

	ui := buildCompactFinalUIMessage(oc.buildStreamUIMessage(state, simpleModeTestMeta("openai/gpt-4.1"), nil))
	if ui == nil {
		t.Fatalf("expected final edit UI message")
	}

	partsAny, ok := ui["parts"].([]any)
	if !ok {
		partsRaw, ok := ui["parts"].([]map[string]any)
		if !ok {
			t.Fatalf("expected parts array, got %T", ui["parts"])
		}
		partsAny = make([]any, 0, len(partsRaw))
		for _, part := range partsRaw {
			partsAny = append(partsAny, part)
		}
	}

	foundSourceURL := false
	foundSourceDocument := false
	foundFile := false
	foundCamelSiteName := false
	foundSnakeSiteName := false
	for _, rawPart := range partsAny {
		part, ok := rawPart.(map[string]any)
		if !ok {
			continue
		}
		switch part["type"] {
		case "source-url":
			foundSourceURL = true
			meta, ok := part["providerMetadata"].(map[string]any)
			if ok {
				_, foundCamelSiteName = meta["siteName"]
				_, foundSnakeSiteName = meta["site_name"]
			}
		case "source-document":
			foundSourceDocument = true
		case "file":
			foundFile = true
		}
	}

	if !foundSourceURL || !foundSourceDocument || !foundFile {
		t.Fatalf("missing expected part types (source-url=%v source-document=%v file=%v)", foundSourceURL, foundSourceDocument, foundFile)
	}
	if !foundCamelSiteName || !foundSnakeSiteName {
		t.Fatalf("expected source-url providerMetadata to include both siteName and site_name (siteName=%v site_name=%v)", foundCamelSiteName, foundSnakeSiteName)
	}
}

func TestBuildFinalEditUIMessage_OmitsTextAndReasoningParts(t *testing.T) {
	oc := &AIClient{}
	state := testStreamingState("turn-2")
	state.accumulated.WriteString("hello")
	state.reasoning.WriteString("thinking")
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{"type": "start", "messageId": "turn-2"})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{"type": "text-start", "id": "text-2"})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{"type": "text-delta", "id": "text-2", "delta": "hello"})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{"type": "text-end", "id": "text-2"})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{"type": "reasoning-start", "id": "reasoning-2"})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{"type": "reasoning-delta", "id": "reasoning-2", "delta": "thinking"})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{"type": "reasoning-end", "id": "reasoning-2"})

	ui := buildCompactFinalUIMessage(oc.buildStreamUIMessage(state, simpleModeTestMeta("openai/gpt-4.1"), nil))
	parts, _ := ui["parts"].([]any)
	for _, rawPart := range parts {
		part, _ := rawPart.(map[string]any)
		switch part["type"] {
		case "text", "reasoning":
			t.Fatalf("expected final UIMessage to omit textual parts, got %#v", part)
		}
	}
}

func TestFinalRenderedBodyFallback_UsesVisibleTurnText(t *testing.T) {
	state := testStreamingState("turn-visible")
	state.accumulated.WriteString("[[reply_to_current]] hidden")
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{"type": "start", "messageId": "turn-visible"})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{"type": "text-start", "id": "text-visible"})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{"type": "text-delta", "id": "text-visible", "delta": "Visible refusal"})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{"type": "text-end", "id": "text-visible"})

	if got := finalRenderedBodyFallback(state); got != "Visible refusal" {
		t.Fatalf("expected visible body fallback, got %q", got)
	}
}

func TestBuildFinalEditTopLevelExtra_KeepsMatrixFallbackFields(t *testing.T) {
	uiMessage := map[string]any{
		"id":   "turn-3",
		"role": "assistant",
	}
	relatesTo := map[string]any{
		"rel_type": "m.replace",
		"event_id": "$orig",
	}

	extra := buildFinalEditTopLevelExtra(uiMessage, nil, relatesTo)

	if _, ok := extra["body"]; ok {
		t.Fatalf("expected body fallback to come from Matrix edit content, got %#v", extra["body"])
	}
	if _, ok := extra["format"]; ok {
		t.Fatalf("expected format fallback to come from Matrix edit content, got %#v", extra["format"])
	}
	if _, ok := extra["formatted_body"]; ok {
		t.Fatalf("expected formatted_body fallback to come from Matrix edit content, got %#v", extra["formatted_body"])
	}
	gotUIMessage, ok := extra[BeeperAIKey].(map[string]any)
	if !ok {
		t.Fatalf("expected UIMessage payload map, got %T", extra[BeeperAIKey])
	}
	if got := gotUIMessage["id"]; got != "turn-3" {
		t.Fatalf("expected UIMessage id, got %#v", got)
	}
	gotRelatesTo, ok := extra["m.relates_to"].(map[string]any)
	if !ok {
		t.Fatalf("expected relates_to map, got %T", extra["m.relates_to"])
	}
	if got := gotRelatesTo["rel_type"]; got != "m.replace" {
		t.Fatalf("expected m.replace relation type, got %#v", got)
	}
	if got := gotRelatesTo["event_id"]; got != "$orig" {
		t.Fatalf("expected relation event id, got %#v", got)
	}
	if _, ok := extra["m.mentions"]; !ok {
		t.Fatalf("expected m.mentions to be present")
	}
}
