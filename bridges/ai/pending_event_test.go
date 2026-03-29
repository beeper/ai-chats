package ai

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestSnapshotPendingEventPreservesReplyTargetAfterSourceMutation(t *testing.T) {
	original := &event.Event{
		ID:     id.EventID("$evt"),
		Sender: id.UserID("@alice:example.com"),
		Content: event.Content{
			Raw: map[string]any{
				"m.relates_to": map[string]any{
					"m.in_reply_to": map[string]any{
						"event_id": "$parent",
					},
				},
			},
		},
	}

	snapshot := snapshotPendingEvent(original)
	original.ID = id.EventID("$mutated")
	original.Sender = id.UserID("@bob:example.com")
	original.Content.Raw["m.relates_to"].(map[string]any)["m.in_reply_to"].(map[string]any)["event_id"] = "$other-parent"

	oc := &AIClient{}
	meta := modelModeTestMeta("openai/gpt-5.2")
	prep, cleanup := oc.prepareStreamingRun(context.Background(), zerolog.Nop(), snapshot, nil, meta)
	defer cleanup()

	if prep.State == nil || prep.State.turn == nil {
		t.Fatalf("expected streaming turn to be prepared")
	}
	if got := prep.State.turn.Source().EventID; got != "$evt" {
		t.Fatalf("expected snapped source event id %q, got %q", "$evt", got)
	}
	if got := prep.State.turn.Source().SenderID; got != "@alice:example.com" {
		t.Fatalf("expected snapped sender id %q, got %q", "@alice:example.com", got)
	}
	if got := prep.State.replyTarget.ReplyTo; got != id.EventID("$parent") {
		t.Fatalf("expected snapped reply target %q, got %q", "$parent", got)
	}
}
