package ai

import (
	"context"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/rs/zerolog"
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

	prep, _, cleanup := oc.prepareStreamingRun(
		context.Background(),
		zerolog.Nop(),
		evt,
		nil,
		meta,
		[]openai.ChatCompletionMessageParamUnion{},
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

	prep, _, cleanup := oc.prepareStreamingRun(
		context.Background(),
		zerolog.Nop(),
		evt,
		nil,
		meta,
		[]openai.ChatCompletionMessageParamUnion{},
	)
	defer cleanup()

	if prep.State == nil {
		t.Fatalf("expected streaming state")
	}
	if prep.State.replyTarget.ReplyTo == "" {
		t.Fatalf("expected reply target to be preserved in agent room")
	}
}
