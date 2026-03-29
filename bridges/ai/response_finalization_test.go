package ai

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

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

	ui := buildCompactFinalUIMessage(oc.buildStreamUIMessage(state, modelModeTestMeta("openai/gpt-4.1"), nil))
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

	ui := buildCompactFinalUIMessage(oc.buildStreamUIMessage(state, modelModeTestMeta("openai/gpt-4.1"), nil))
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

func TestBuildFinalEditTopLevelExtra_KeepsOnlyEditMetadata(t *testing.T) {
	uiMessage := map[string]any{
		"id":   "turn-3",
		"role": "assistant",
	}
	previews := []*event.BeeperLinkPreview{{
		MatchedURL: "https://example.com",
	}}

	extra := buildFinalEditTopLevelExtra()

	if _, ok := extra["body"]; ok {
		t.Fatalf("expected body fallback to come from Matrix edit content, got %#v", extra["body"])
	}
	if _, ok := extra["format"]; ok {
		t.Fatalf("expected format fallback to come from Matrix edit content, got %#v", extra["format"])
	}
	if _, ok := extra["formatted_body"]; ok {
		t.Fatalf("expected formatted_body fallback to come from Matrix edit content, got %#v", extra["formatted_body"])
	}
	if _, ok := extra[BeeperAIKey]; ok {
		t.Fatalf("expected UIMessage payload to move into m.new_content, got %#v", extra[BeeperAIKey])
	}
	if _, ok := extra["com.beeper.linkpreviews"]; ok {
		t.Fatalf("expected link previews to move into m.new_content, got %#v", extra["com.beeper.linkpreviews"])
	}
	if _, ok := extra["m.relates_to"]; ok {
		t.Fatalf("expected SDK to inject m.relates_to, got %#v", extra["m.relates_to"])
	}
	if uiMessage["id"] != "turn-3" || previews[0].MatchedURL != "https://example.com" {
		t.Fatalf("expected helper inputs to remain untouched")
	}
}

func TestBuildFinalEditPayloadMovesCanonicalFieldsIntoNewContent(t *testing.T) {
	topLevelExtra := map[string]any{
		"com.beeper.ai":                 map[string]any{"id": "turn-4"},
		"com.beeper.linkpreviews":       []map[string]any{{"matched_url": "https://example.com"}},
		"com.beeper.dont_render_edited": true,
	}

	payload := buildFinalEditPayload(event.MessageEventContent{
		MsgType:       event.MsgText,
		Body:          "done",
		Format:        event.FormatHTML,
		FormattedBody: "<p>done</p>",
		RelatesTo:     (&event.RelatesTo{}).SetReplyTo(id.EventID("$reply")),
		Mentions: &event.Mentions{
			UserIDs: []id.UserID{"@alice:example.com"},
		},
	}, topLevelExtra)
	if payload == nil || payload.Content == nil {
		t.Fatalf("expected final edit payload")
	}
	if payload.Content.Body != "done" {
		t.Fatalf("expected payload body to be preserved, got %q", payload.Content.Body)
	}
	if payload.Content.RelatesTo != nil {
		t.Fatalf("expected relation to be stripped from replacement content, got %#v", payload.Content.RelatesTo)
	}
	if payload.Content.Mentions == nil || len(payload.Content.Mentions.UserIDs) != 1 {
		t.Fatalf("expected mentions to be preserved, got %#v", payload.Content.Mentions)
	}
	if payload.Extra[BeeperAIKey] == nil {
		t.Fatalf("expected UI message payload in m.new_content extra, got %#v", payload.Extra)
	}
	if payload.Extra["com.beeper.linkpreviews"] == nil {
		t.Fatalf("expected link previews in m.new_content extra, got %#v", payload.Extra)
	}
	if payload.TopLevelExtra["com.beeper.dont_render_edited"] != true {
		t.Fatalf("expected dont_render_edited to stay top-level, got %#v", payload.TopLevelExtra)
	}
	if _, ok := payload.TopLevelExtra[BeeperAIKey]; ok {
		t.Fatalf("expected UI message payload to be absent from top-level extra, got %#v", payload.TopLevelExtra[BeeperAIKey])
	}
}
