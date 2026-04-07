package sdk

import (
	"strings"
	"testing"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/matrixevents"
)

func TestFitFinalEditPayloadCompactsOptionalMetadataFirst(t *testing.T) {
	largePart := map[string]any{
		"type": "tool-call",
		"text": strings.Repeat("x", MaxMatrixEventContentBytes),
	}
	payload := &FinalEditPayload{
		Content: &event.MessageEventContent{
			MsgType:       event.MsgText,
			Body:          "done",
			Format:        event.FormatHTML,
			FormattedBody: strings.Repeat("<p>done</p>", MaxMatrixEventContentBytes/8),
		},
		Extra: map[string]any{
			matrixevents.BeeperAIKey: map[string]any{
				"id":       "turn-1",
				"role":     "assistant",
				"metadata": map[string]any{"finish_reason": "stop"},
				"parts":    []any{largePart},
			},
			"com.beeper.linkpreviews": []map[string]any{{
				"matched_url": "https://example.com",
				"title":       strings.Repeat("preview", 2000),
			}},
		},
		TopLevelExtra: map[string]any{
			"com.beeper.dont_render_edited": true,
		},
	}

	fitted, details, err := FitFinalEditPayload(payload, id.EventID("$event-1"))
	if err != nil {
		t.Fatalf("expected fit to succeed, got %v", err)
	}
	if fitted == nil || fitted.Content == nil {
		t.Fatal("expected fitted payload")
	}
	if details.FinalSize > MaxMatrixEventContentBytes {
		t.Fatalf("expected fitted payload under %d bytes, got %d", MaxMatrixEventContentBytes, details.FinalSize)
	}
	if fitted.Content.Body != "done" {
		t.Fatalf("expected body to be preserved, got %q", fitted.Content.Body)
	}
	if !details.ClearedFormattedBody {
		t.Fatal("expected formatted body to be cleared before trimming body")
	}
	if !details.DroppedLinkPreviews {
		t.Fatal("expected oversized link previews to be dropped")
	}
	if details.TrimmedBody {
		t.Fatal("expected metadata reductions to avoid trimming the visible body")
	}
	if rawUI, ok := fitted.Extra[matrixevents.BeeperAIKey].(map[string]any); ok {
		if _, ok = rawUI["parts"]; ok {
			t.Fatalf("expected ui message parts to be removed, got %#v", rawUI["parts"])
		}
	}
}

func TestFitFinalEditPayloadDeepClonesNestedMaps(t *testing.T) {
	payload := &FinalEditPayload{
		Content: &event.MessageEventContent{
			MsgType:       event.MsgText,
			Body:          "done",
			Format:        event.FormatHTML,
			FormattedBody: strings.Repeat("x", MaxMatrixEventContentBytes*2),
		},
		Extra: map[string]any{
			"nested": map[string]any{
				"value": "original",
			},
		},
		TopLevelExtra: map[string]any{
			"outer": map[string]any{
				"flag": true,
			},
		},
	}

	fitted, details, err := FitFinalEditPayload(payload, id.EventID("$event-clone"))
	if err != nil {
		t.Fatalf("expected fit to succeed, got %v; details: %+v", err, details)
	}
	if fitted == nil || fitted.Content == nil {
		t.Fatal("expected fitted payload")
	}
	t.Logf("details: %+v", details)
	t.Logf("fitted.Extra: %v", fitted.Extra)
	t.Logf("fitted.TopLevelExtra: %v", fitted.TopLevelExtra)

	if fitted.Extra == nil {
		t.Fatal("expected Extra to survive fitting")
	}
	fitted.Extra["nested"].(map[string]any)["value"] = "changed"
	fitted.TopLevelExtra["outer"].(map[string]any)["flag"] = false

	if got := payload.Extra["nested"].(map[string]any)["value"]; got != "original" {
		t.Fatalf("expected original nested extra to stay unchanged, got %#v", got)
	}
	if got := payload.TopLevelExtra["outer"].(map[string]any)["flag"]; got != true {
		t.Fatalf("expected original nested top-level extra to stay unchanged, got %#v", got)
	}
}

func TestFitFinalEditPayloadReturnsErrorWhenPayloadCannotFit(t *testing.T) {
	payload := &FinalEditPayload{
		Content: &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    "done",
		},
		TopLevelExtra: map[string]any{
			"com.beeper.dont_render_edited": true,
			"huge":                          strings.Repeat("x", MaxMatrixEventContentBytes),
		},
	}

	fitted, details, err := FitFinalEditPayload(payload, id.EventID("$event-too-large"))
	if err == nil {
		t.Fatal("expected fit to fail for unshrinkable payload")
	}
	if fitted != nil {
		t.Fatalf("expected no fitted payload on failure, got %#v", fitted)
	}
	if details.FinalSize <= MaxMatrixEventContentBytes {
		t.Fatalf("expected final size to remain oversized, got %d", details.FinalSize)
	}
}

func TestFitFinalEditPayloadTrimsBodyAsLastResort(t *testing.T) {
	body := strings.Repeat("abc\n", MaxMatrixEventContentBytes)
	payload := &FinalEditPayload{
		Content: &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    body,
		},
		TopLevelExtra: map[string]any{
			"com.beeper.dont_render_edited": true,
		},
	}

	fitted, details, err := FitFinalEditPayload(payload, id.EventID("$event-2"))
	if err != nil {
		t.Fatalf("expected fit to succeed, got %v", err)
	}
	if fitted == nil || fitted.Content == nil {
		t.Fatal("expected fitted payload")
	}
	if !details.TrimmedBody {
		t.Fatal("expected oversized body to be trimmed")
	}
	if details.FinalSize > MaxMatrixEventContentBytes {
		t.Fatalf("expected fitted payload under %d bytes, got %d", MaxMatrixEventContentBytes, details.FinalSize)
	}
	if len(fitted.Content.Body) >= len(body) {
		t.Fatalf("expected trimmed body to be shorter than original")
	}
}

func TestFitFinalEditPayloadBinarySearchUsesOriginalBody(t *testing.T) {
	paragraphOne := strings.Repeat("a", 25000)
	paragraphTwo := strings.Repeat("b", 25000)
	paragraphThree := strings.Repeat("c", 25000)
	body := paragraphOne + "\n\n" + paragraphTwo + "\n\n" + paragraphThree
	payload := &FinalEditPayload{
		Content: &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    body,
		},
		TopLevelExtra: map[string]any{
			"com.beeper.dont_render_edited": true,
		},
	}

	fitted, details, err := FitFinalEditPayload(payload, id.EventID("$event-boundary"))
	if err != nil {
		t.Fatalf("expected fit to succeed, got %v", err)
	}
	if fitted == nil || fitted.Content == nil {
		t.Fatal("expected fitted payload")
	}
	want := paragraphOne + "\n\n" + paragraphTwo
	if fitted.Content.Body != want {
		t.Fatalf("expected trimmed body to retain the largest markdown-safe prefix, got len=%d want len=%d", len(fitted.Content.Body), len(want))
	}
	if !details.TrimmedBody {
		t.Fatal("expected body trimming details to be reported")
	}
}
