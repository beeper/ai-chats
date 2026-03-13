package sdk

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/id"
)

func TestTurnBuildRelatesToDefaultsToSourceEvent(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, UserMessageSource("$source"))
	rel := turn.buildRelatesTo()
	if rel == nil || rel["event_id"] != "$source" {
		t.Fatalf("expected source event relation, got %#v", rel)
	}
}

func TestTurnBuildRelatesToPrefersReplyAndThread(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, UserMessageSource("$source"))
	turn.SetReplyTo(id.EventID("$reply"))
	rel := turn.buildRelatesTo()
	inReply, ok := rel["m.in_reply_to"].(map[string]any)
	if !ok || inReply["event_id"] != "$reply" {
		t.Fatalf("expected explicit reply relation, got %#v", rel)
	}

	turn.SetThread(id.EventID("$thread"))
	rel = turn.buildRelatesTo()
	if rel["event_id"] != "$thread" {
		t.Fatalf("expected thread root relation, got %#v", rel)
	}
	inReply, ok = rel["m.in_reply_to"].(map[string]any)
	if !ok || inReply["event_id"] != "$reply" {
		t.Fatalf("expected thread fallback reply, got %#v", rel)
	}
}
