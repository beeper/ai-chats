package turns

import (
	"testing"

	"maunium.net/go/mautrix/event"
)

func TestBuildDebouncedEditContent_WithoutEventID(t *testing.T) {
	content := BuildDebouncedEditContent(DebouncedEditParams{
		PortalMXID:  "test-room",
		VisibleBody: "hello",
	})
	if content == nil {
		t.Fatal("expected debounced edit content without event ID")
	}
	if content.Body == "" {
		t.Fatal("expected non-empty body")
	}
}

func TestBuildDebouncedEditContent_NotForcedStillRenders(t *testing.T) {
	content := BuildDebouncedEditContent(DebouncedEditParams{
		PortalMXID:  "test-room",
		Force:       false,
		VisibleBody: "**hello**",
	})
	if content == nil {
		t.Fatal("expected debounced edit content for non-forced update")
	}
	if content.FormattedBody == "" {
		t.Fatal("expected formatted html body")
	}
}

func TestDebouncedPartMode_ToolEventsEligible(t *testing.T) {
	toolForceEvents := []string{
		"tool-input-start", "tool-input-available", "tool-input-error",
		"tool-output-available", "tool-output-error", "tool-output-denied",
	}
	for _, partType := range toolForceEvents {
		eligible, force := debouncedPartMode(partType)
		if !eligible {
			t.Errorf("expected %q to be debounce-eligible", partType)
		}
		if !force {
			t.Errorf("expected %q to force immediate debounced send", partType)
		}
	}

	eligible, force := debouncedPartMode("tool-input-delta")
	if !eligible {
		t.Error("expected tool-input-delta to be debounce-eligible")
	}
	if force {
		t.Error("expected tool-input-delta to NOT force immediate send")
	}

	for _, partType := range []string{"tool-approval-request", "tool-approval-response"} {
		eligible, force := debouncedPartMode(partType)
		if eligible {
			t.Errorf("expected %q to be ineligible for debounced edits", partType)
		}
		if force {
			t.Errorf("expected %q to not force debounced sends", partType)
		}
	}

	eligible, _ = debouncedPartMode("unknown-part-type")
	if eligible {
		t.Error("expected unknown part type to be ineligible")
	}
}

func TestBuildConvertedEdit_KeepsOnlyCustomTopLevelFields(t *testing.T) {
	edit := BuildConvertedEdit(&event.MessageEventContent{
		MsgType:       event.MsgText,
		Body:          "Hello",
		Format:        event.FormatHTML,
		FormattedBody: "<p>Hello</p>",
	}, map[string]any{
		"com.beeper.dont_render_edited": true,
	})
	if edit == nil || len(edit.ModifiedParts) != 1 {
		t.Fatal("expected single modified part")
	}
	extra := edit.ModifiedParts[0].TopLevelExtra
	if extra["com.beeper.dont_render_edited"] != true {
		t.Fatalf("expected custom top-level flag, got %#v", extra["com.beeper.dont_render_edited"])
	}
	if _, ok := extra["body"]; ok {
		t.Fatalf("expected body fallback to be synthesized from Content, got %#v", extra["body"])
	}
	if _, ok := extra["format"]; ok {
		t.Fatalf("expected format fallback to be synthesized from Content, got %#v", extra["format"])
	}
	if _, ok := extra["formatted_body"]; ok {
		t.Fatalf("expected formatted_body fallback to be synthesized from Content, got %#v", extra["formatted_body"])
	}
}
