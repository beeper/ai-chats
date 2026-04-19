package ai

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestPrepareStreamingRun_ModelRoomKeepsReplyTarget(t *testing.T) {
	oc := &AIClient{}
	meta := modelModeTestMeta("openai/gpt-5.2")
	evt := &event.Event{
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

	prep, cleanup := oc.prepareStreamingRun(
		context.Background(),
		zerolog.Nop(),
		evt,
		nil,
		meta,
	)
	defer cleanup()

	if prep.State == nil {
		t.Fatalf("expected streaming state")
	}
	if prep.State.replyTarget.ReplyTo == "" {
		t.Fatalf("expected reply target to be preserved in model room, got %+v", prep.State.replyTarget)
	}
}

func TestPrepareStreamingRun_AgentRoomKeepsReplyTarget(t *testing.T) {
	oc := &AIClient{}
	meta := agentModeTestMeta("beeper")
	evt := &event.Event{
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

	prep, cleanup := oc.prepareStreamingRun(
		context.Background(),
		zerolog.Nop(),
		evt,
		nil,
		meta,
	)
	defer cleanup()

	if prep.State == nil {
		t.Fatalf("expected streaming state")
	}
	if prep.State.replyTarget.ReplyTo == "" {
		t.Fatalf("expected reply target to be preserved in agent room")
	}
}

func TestPrepareStreamingRun_SnapshotsResponderFields(t *testing.T) {
	oc := newTestAIClientWithProvider("")
	oc.connector = &OpenAIConnector{}
	setTestLoginState(oc, &loginRuntimeState{
		ModelCache: &ModelCache{Models: []ModelInfo{{
			ID:            "openai/gpt-5.2",
			ContextWindow: 400000,
		}}},
	})
	meta := modelModeTestMeta("openai/gpt-5.2")

	prep, cleanup := oc.prepareStreamingRun(
		context.Background(),
		zerolog.Nop(),
		nil,
		nil,
		meta,
	)
	defer cleanup()

	if prep.State == nil {
		t.Fatalf("expected streaming state")
	}
	if prep.State.respondingModelID != "openai/gpt-5.2" {
		t.Fatalf("expected responder model snapshot, got %q", prep.State.respondingModelID)
	}
	if prep.State.respondingContextLimit != 400000 {
		t.Fatalf("expected responder context snapshot, got %d", prep.State.respondingContextLimit)
	}

	meta.ResolvedTarget.ModelID = "openai/gpt-4.1"
	if prep.State.respondingModelID != "openai/gpt-5.2" {
		t.Fatalf("expected snapshot to remain stable after metadata mutation, got %q", prep.State.respondingModelID)
	}
}

func TestMarkTurnAcceptedStartsPlaceholderOnce(t *testing.T) {
	oc := &AIClient{}
	prep, cleanup := oc.prepareStreamingRun(
		context.Background(),
		zerolog.Nop(),
		nil,
		nil,
		nil,
	)
	defer cleanup()

	if prep.State == nil || prep.State.turn == nil {
		t.Fatalf("expected streaming turn state")
	}

	sendCalls := 0
	prep.State.turn.SetSendFunc(func(context.Context) (id.EventID, networkid.MessageID, error) {
		sendCalls++
		return id.EventID("$placeholder"), networkid.MessageID("msg-1"), nil
	})

	if sendCalls != 0 {
		t.Fatalf("expected no placeholder send before acceptance")
	}
	if prep.State.turn.InitialEventID() != "" {
		t.Fatalf("expected no initial event before acceptance")
	}

	oc.markTurnAccepted(context.Background(), nil, prep.State, nil)
	if sendCalls != 1 {
		t.Fatalf("expected placeholder send on acceptance, got %d", sendCalls)
	}
	if prep.State.turn.InitialEventID() != id.EventID("$placeholder") {
		t.Fatalf("unexpected initial event after acceptance: %q", prep.State.turn.InitialEventID())
	}

	oc.markTurnAccepted(context.Background(), nil, prep.State, nil)
	if sendCalls != 1 {
		t.Fatalf("expected placeholder send to stay idempotent, got %d sends", sendCalls)
	}
}
