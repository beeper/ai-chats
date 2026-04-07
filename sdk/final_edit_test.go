package sdk

import (
	"strings"
	"testing"

	"github.com/beeper/agentremote/pkg/matrixevents"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
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

	fitted, details := FitFinalEditPayload(payload, id.EventID("$event-1"))
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

	fitted, details := FitFinalEditPayload(payload, id.EventID("$event-2"))
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
