package aihelpers

import (
	"context"
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-chats/pkg/matrixevents"
	"github.com/beeper/ai-chats/pkg/shared/citations"
	"github.com/beeper/ai-chats/pkg/shared/turns"
)

func TestTurnBuildFinalEditAddsReplaceRelation(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.initialEventID = id.EventID("$event-1")
	turn.networkMessageID = "msg-1"
	turn.Writer().TextDelta(turn.Context(), "streamed")
	turn.SetFinalEditPayload(&FinalEditPayload{
		Content: &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    "done",
		},
		Extra: map[string]any{
			"com.beeper.ai": map[string]any{"id": turn.ID()},
		},
		TopLevelExtra: map[string]any{
			"com.beeper.dont_render_edited": true,
		},
	})

	target, edit := turn.buildFinalEdit()
	if target != "msg-1" {
		t.Fatalf("expected network target msg-1, got %q", target)
	}
	if edit == nil || len(edit.ModifiedParts) != 1 {
		t.Fatalf("expected single modified part, got %#v", edit)
	}
	gotRelatesTo, ok := edit.ModifiedParts[0].TopLevelExtra["m.relates_to"].(*event.RelatesTo)
	if !ok {
		t.Fatalf("expected m.relates_to in top-level extra, got %#v", edit.ModifiedParts[0].TopLevelExtra)
	}
	if gotRelatesTo.Type != event.RelReplace {
		t.Fatalf("expected m.replace relation, got %#v", gotRelatesTo)
	}
	if gotRelatesTo.EventID != id.EventID("$event-1") {
		t.Fatalf("expected replace target event id, got %#v", gotRelatesTo)
	}
	if gotRelatesTo.InReplyTo != nil {
		t.Fatalf("expected edit relation to omit reply override, got %#v", gotRelatesTo)
	}
	if body := edit.ModifiedParts[0].Content.Body; body != "done" {
		t.Fatalf("expected explicit payload body to win, got %q", body)
	}
	if edit.ModifiedParts[0].Content.Mentions == nil {
		t.Fatalf("expected typed mentions on edited content")
	}
	if edit.ModifiedParts[0].Content.RelatesTo != nil {
		t.Fatalf("expected replacement content to omit reply/thread relation, got %#v", edit.ModifiedParts[0].Content.RelatesTo)
	}
	rawAI, ok := edit.ModifiedParts[0].Extra[matrixevents.BeeperAIKey].(map[string]any)
	if !ok {
		t.Fatalf("expected %s payload in edit extra, got %#v", matrixevents.BeeperAIKey, edit.ModifiedParts[0].Extra)
	}
	if rawAI["id"] != turn.ID() {
		t.Fatalf("expected ai payload id %q, got %#v", turn.ID(), rawAI["id"])
	}
}

func TestTurnBuildFinalEditStripsRelationFromNewContent(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.initialEventID = id.EventID("$event-1")
	turn.networkMessageID = "msg-1"
	turn.SetFinalEditPayload(&FinalEditPayload{
		Content: &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    "done",
			RelatesTo: (&event.RelatesTo{}).
				SetThread(id.EventID("$thread-1"), id.EventID("$reply-1")),
		},
	})

	_, edit := turn.buildFinalEdit()
	if edit == nil || len(edit.ModifiedParts) != 1 {
		t.Fatalf("expected single modified part, got %#v", edit)
	}
	if rel := edit.ModifiedParts[0].Content.RelatesTo; rel != nil {
		t.Fatalf("expected edited content to strip m.relates_to, got %#v", rel)
	}
}

func TestTurnAwaitStreamStartStopsOnPermanentError(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.session = turns.NewStreamSession(turns.StreamSessionParams{
		TurnID: "turn-no-publisher",
		GetRoomID: func() id.RoomID {
			return id.RoomID("!room:test")
		},
		GetTargetEventID: func() id.EventID {
			return id.EventID("$event-no-publisher")
		},
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		turn.awaitStreamStart()
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected awaitStreamStart to stop on permanent error")
	}
}

func TestTurnBuildFinalEditDefaultsToVisibleText(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.initialEventID = id.EventID("$event-text")
	turn.networkMessageID = "msg-text"
	turn.Writer().TextDelta(turn.Context(), "hello")
	turn.Writer().FinishText(turn.Context())
	turn.ensureDefaultFinalEditPayload("stop", "")

	target, edit := turn.buildFinalEdit()
	if target != "msg-text" {
		t.Fatalf("expected network target msg-text, got %q", target)
	}
	if edit == nil || len(edit.ModifiedParts) != 1 {
		t.Fatalf("expected single modified part, got %#v", edit)
	}
	if body := edit.ModifiedParts[0].Content.Body; body != "hello" {
		t.Fatalf("expected visible text body, got %q", body)
	}
	extra := edit.ModifiedParts[0].TopLevelExtra
	if extra["com.beeper.dont_render_edited"] != true {
		t.Fatalf("expected dont_render_edited marker, got %#v", extra)
	}
	if _, ok := extra[matrixevents.BeeperAIKey]; ok {
		t.Fatalf("expected compact %s payload outside top-level extra, got %#v", matrixevents.BeeperAIKey, extra[matrixevents.BeeperAIKey])
	}
	rawAI, ok := edit.ModifiedParts[0].Extra[matrixevents.BeeperAIKey].(map[string]any)
	if !ok {
		t.Fatalf("expected compact %s payload, got %#v", matrixevents.BeeperAIKey, edit.ModifiedParts[0].Extra)
	}
	if parts, ok := rawAI["parts"].([]any); ok {
		for _, raw := range parts {
			part, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if partType := strings.TrimSpace(stringValue(part["type"])); partType == "text" {
				t.Fatalf("expected compact final payload without duplicate text parts, got %#v", part)
			}
		}
	}
	metadata, _ := rawAI["metadata"].(map[string]any)
	if metadata["finish_reason"] != "stop" {
		t.Fatalf("expected synthesized finish_reason metadata, got %#v", metadata)
	}
}

func TestTurnFinalizeTurnEndsStreamBeforeDispatchingFinalEdit(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.session = turns.NewStreamSession(turns.StreamSessionParams{
		TurnID: "turn-finalize-order",
	})

	if turn.session.IsClosed() {
		t.Fatal("expected stream session to start open")
	}

	finalEditDispatched := false
	turn.sendFinalEditFunc = func(context.Context) {
		finalEditDispatched = true
		if !turn.session.IsClosed() {
			t.Fatal("expected stream session to be closed before dispatching final edit")
		}
	}

	turn.finalizeTurn(turns.EndReasonFinish, "stop", "")

	if !finalEditDispatched {
		t.Fatal("expected final edit dispatch hook to run")
	}
	if !turn.session.IsClosed() {
		t.Fatal("expected stream session to remain closed after finalization")
	}
}

func TestTurnBuildFinalEditDefaultsToGenericBodyForArtifacts(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.initialEventID = id.EventID("$event-artifact")
	turn.networkMessageID = "msg-artifact"
	turn.Writer().SourceURL(turn.Context(), citations.SourceCitation{
		URL:   "https://example.com",
		Title: "Example",
	})
	turn.ensureDefaultFinalEditPayload("stop", "")

	_, edit := turn.buildFinalEdit()
	if edit == nil || len(edit.ModifiedParts) != 1 {
		t.Fatalf("expected single modified part, got %#v", edit)
	}
	if body := edit.ModifiedParts[0].Content.Body; body != "Completed response" {
		t.Fatalf("expected generic body for artifact-only turn, got %q", body)
	}
	rawAI, ok := edit.ModifiedParts[0].Extra[matrixevents.BeeperAIKey].(map[string]any)
	if !ok {
		t.Fatalf("expected compact ai payload, got %#v", edit.ModifiedParts[0].Extra)
	}
	parts, _ := rawAI["parts"].([]any)
	if len(parts) == 0 {
		t.Fatalf("expected artifact part in compact payload, got %#v", rawAI)
	}
}

func TestTurnBuildFinalEditClearsStreamDescriptorWhenSessionExists(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.initialEventID = id.EventID("$event-stream")
	turn.networkMessageID = "msg-stream"
	turn.session = turns.NewStreamSession(turns.StreamSessionParams{
		TurnID: "turn-stream-clear",
	})
	turn.SetFinalEditPayload(&FinalEditPayload{
		Content: &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    "done",
		},
	})

	_, edit := turn.buildFinalEdit()
	if edit == nil || len(edit.ModifiedParts) != 1 {
		t.Fatalf("expected single modified part, got %#v", edit)
	}
	part := edit.ModifiedParts[0]
	if _, ok := part.Extra["com.beeper.stream"]; !ok {
		t.Fatalf("expected m.new_content to explicitly clear com.beeper.stream, got %#v", part.Extra)
	}
	if part.Extra["com.beeper.stream"] != nil {
		t.Fatalf("expected com.beeper.stream to be cleared with nil, got %#v", part.Extra["com.beeper.stream"])
	}
	if _, ok := part.TopLevelExtra["com.beeper.stream"]; !ok {
		t.Fatalf("expected top-level edit content to explicitly clear com.beeper.stream, got %#v", part.TopLevelExtra)
	}
	if part.TopLevelExtra["com.beeper.stream"] != nil {
		t.Fatalf("expected top-level com.beeper.stream to be cleared with nil, got %#v", part.TopLevelExtra["com.beeper.stream"])
	}
}

func TestTurnBuildFinalEditPreservesMentionsInContent(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.initialEventID = id.EventID("$event-mentions")
	turn.networkMessageID = "msg-mentions"
	turn.SetFinalEditPayload(&FinalEditPayload{
		Content: &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    "hi",
			Mentions: &event.Mentions{
				UserIDs: []id.UserID{"@alice:test"},
			},
		},
		TopLevelExtra: map[string]any{
			"com.beeper.dont_render_edited": true,
		},
	})

	_, edit := turn.buildFinalEdit()
	if edit == nil || len(edit.ModifiedParts) != 1 {
		t.Fatalf("expected single modified part, got %#v", edit)
	}
	mentions := edit.ModifiedParts[0].Content.Mentions
	if mentions == nil || len(mentions.UserIDs) != 1 || mentions.UserIDs[0] != id.UserID("@alice:test") {
		t.Fatalf("expected mentions to be preserved in replacement content, got %#v", mentions)
	}
}

func TestTurnBuildFinalEditSkipsUnshrinkablePayload(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.initialEventID = id.EventID("$event-too-large")
	turn.networkMessageID = "msg-too-large"
	turn.SetFinalEditPayload(&FinalEditPayload{
		Content: &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    "done",
			Mentions: &event.Mentions{
				UserIDs: []id.UserID{
					id.UserID("@" + strings.Repeat("x", MaxMatrixEventContentBytes) + ":test"),
				},
			},
		},
	})

	target, edit := turn.buildFinalEdit()
	if target != "" || edit != nil {
		t.Fatalf("expected oversized final edit to be skipped, got target=%q edit=%#v", target, edit)
	}
}

func TestTurnBuildFinalEditFallsBackToTextOnlyPayload(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.initialEventID = id.EventID("$event-fallback")
	turn.networkMessageID = "msg-fallback"
	turn.SetFinalEditPayload(&FinalEditPayload{
		Content: &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    "done",
		},
		Extra: map[string]any{
			matrixevents.BeeperAIKey: map[string]any{
				"id": "turn-1",
			},
		},
		TopLevelExtra: map[string]any{
			"com.beeper.dont_render_edited": true,
			"huge":                          strings.Repeat("x", MaxMatrixEventContentBytes),
		},
	})

	target, edit := turn.buildFinalEdit()
	if target != "msg-fallback" {
		t.Fatalf("expected fallback edit target msg-fallback, got %q", target)
	}
	if edit == nil || len(edit.ModifiedParts) != 1 {
		t.Fatalf("expected single modified part, got %#v", edit)
	}
	if got := edit.ModifiedParts[0].Content.Body; got != "done" {
		t.Fatalf("expected fallback to preserve visible body, got %q", got)
	}
	if _, ok := edit.ModifiedParts[0].Extra[matrixevents.BeeperAIKey]; ok {
		t.Fatalf("expected text-only fallback to strip extra metadata, got %#v", edit.ModifiedParts[0].Extra)
	}
	if _, ok := edit.ModifiedParts[0].TopLevelExtra["com.beeper.dont_render_edited"]; ok {
		t.Fatalf("expected text-only fallback to drop optional top-level metadata, got %#v", edit.ModifiedParts[0].TopLevelExtra)
	}
	gotRelatesTo, ok := edit.ModifiedParts[0].TopLevelExtra["m.relates_to"].(*event.RelatesTo)
	if !ok {
		t.Fatalf("expected fallback edit to restore replace relation, got %#v", edit.ModifiedParts[0].TopLevelExtra)
	}
	if gotRelatesTo.EventID != id.EventID("$event-fallback") || gotRelatesTo.Type != event.RelReplace {
		t.Fatalf("expected replace relation for original event, got %#v", gotRelatesTo)
	}
}

func TestTurnFinalizationContextPrefersActiveTurnContext(t *testing.T) {
	type ctxKey string
	const key ctxKey = "source"

	parent := context.WithValue(context.Background(), key, "turn")
	turn := newTurn(parent, nil, nil, nil)

	got := turn.finalizationContext()
	if got == nil {
		t.Fatal("expected finalization context")
	}
	if got != turn.Context() {
		t.Fatal("expected active turn context to be reused for finalization")
	}
	if got.Value(key) != "turn" {
		t.Fatalf("expected turn context value, got %#v", got.Value(key))
	}
}

func TestTurnFinalizationContextFallsBackToBridgeBackground(t *testing.T) {
	type ctxKey string
	const key ctxKey = "source"

	bridgeCtx := context.WithValue(context.Background(), key, "bridge")
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key, "parent"))
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{ID: "login-1"},
		Bridge:    &bridgev2.Bridge{BackgroundCtx: bridgeCtx},
	}
	conv := newConversation(parent, &bridgev2.Portal{Portal: &database.Portal{}}, login, bridgev2.EventSender{})
	turn := newTurn(parent, conv, nil, nil)

	cancel()
	if turn.Context().Err() == nil {
		t.Fatal("expected turn context to be cancelled")
	}

	got := turn.finalizationContext()
	if got == nil {
		t.Fatal("expected fallback finalization context")
	}
	if got.Err() != nil {
		t.Fatalf("expected active fallback context, got err=%v", got.Err())
	}
	if got == turn.Context() {
		t.Fatal("expected fallback context instead of cancelled turn context")
	}
	if got.Value(key) != "bridge" {
		t.Fatalf("expected bridge background context, got %#v", got.Value(key))
	}
}

func TestTurnSuppressFinalEditSkipsAutomaticPayload(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.initialEventID = id.EventID("$event-suppressed")
	turn.networkMessageID = "msg-suppressed"
	turn.Writer().TextDelta(turn.Context(), "hello")
	turn.SetSuppressFinalEdit(true)
	turn.ensureDefaultFinalEditPayload("stop", "")

	target, edit := turn.buildFinalEdit()
	if target != "" || edit != nil {
		t.Fatalf("expected automatic final edit to be suppressed, got target=%q edit=%#v", target, edit)
	}
}

func TestTurnBuildFinalEditDoesNotSynthesizeForMetadataOnlyTurn(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.initialEventID = id.EventID("$event-meta")
	turn.networkMessageID = "msg-meta"
	turn.Writer().Start(turn.Context(), map[string]any{"turnId": turn.ID()})
	turn.ensureDefaultFinalEditPayload("stop", "")

	target, edit := turn.buildFinalEdit()
	if target != "" || edit != nil {
		t.Fatalf("expected no synthesized edit for metadata-only turn, got target=%q edit=%#v", target, edit)
	}
}

func TestTurnBuildFinalEditUsesErrorTextFallback(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.initialEventID = id.EventID("$event-error")
	turn.networkMessageID = "msg-error"
	turn.Writer().Error(turn.Context(), "boom")
	turn.ensureDefaultFinalEditPayload("error", "boom")

	_, edit := turn.buildFinalEdit()
	if edit == nil || len(edit.ModifiedParts) != 1 {
		t.Fatalf("expected synthesized error edit, got %#v", edit)
	}
	if body := edit.ModifiedParts[0].Content.Body; body != "boom" {
		t.Fatalf("expected error text fallback body, got %q", body)
	}
}
