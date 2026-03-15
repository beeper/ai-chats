package ai

import (
	"context"
	"errors"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/rs/zerolog"

	"github.com/beeper/agentremote/pkg/shared/streamui"
)

func TestChatCompletionsHandleStreamStepErrorFinalizesContextLength(t *testing.T) {
	state := newTestStreamingStateWithTurn()
	state.turn.SetSuppressSend(true)

	adapter := &chatCompletionsTurnAdapter{
		agentLoopProviderBase: agentLoopProviderBase{
			oc:    &AIClient{},
			log:   zerolog.Nop(),
			state: state,
		},
	}
	stepErr := errors.New("This model's maximum context length is 100 tokens. However, your messages resulted in 120 tokens.")

	cle, err := adapter.handleStreamStepError(context.Background(), openai.ChatCompletionNewParams{}, nil, stepErr)
	if cle == nil {
		t.Fatal("expected context-length error")
	}
	if err == nil {
		t.Fatal("expected stream finalization error")
	}
	var preDelta *PreDeltaError
	if !errors.As(err, &preDelta) {
		t.Fatalf("expected PreDeltaError wrapper, got %T", err)
	}
	if state.finishReason != "context-length" {
		t.Fatalf("expected finish reason to be context-length, got %q", state.finishReason)
	}
	if state.completedAtMs == 0 {
		t.Fatal("expected completion timestamp to be set")
	}
}

func TestBuildStreamingMessageMetadataHandlesNilTurn(t *testing.T) {
	state := newStreamingState(context.Background(), nil, "")

	meta := (&AIClient{}).buildStreamingMessageMetadata(state, nil, nil)
	if meta == nil {
		t.Fatal("expected metadata")
	}
	if meta.TurnID != "" {
		t.Fatalf("expected empty turn id, got %q", meta.TurnID)
	}
	if len(meta.CanonicalTurnData) != 0 {
		t.Fatalf("expected no canonical turn data without a turn, got %#v", meta.CanonicalTurnData)
	}
}

func TestHandleResponseLifecycleEventEmitsMetadataForCompleted(t *testing.T) {
	state := newTestStreamingStateWithTurn()
	oc := &AIClient{}

	state.writer().Start(context.Background(), map[string]any{
		"turn_id": state.turn.ID(),
	})

	oc.handleResponseLifecycleEvent(context.Background(), nil, state, nil, "response.completed", responses.Response{
		ID:     "resp_123",
		Status: "completed",
		Model:  "gpt-4.1",
	})

	message := streamui.SnapshotUIMessage(state.turn.UIState())
	if message == nil {
		t.Fatal("expected UI message snapshot")
	}
	metadata, _ := message["metadata"].(map[string]any)
	if metadata["response_id"] != "resp_123" {
		t.Fatalf("expected response_id metadata, got %#v", metadata["response_id"])
	}
	if metadata["response_status"] != "completed" {
		t.Fatalf("expected response_status metadata, got %#v", metadata["response_status"])
	}
	if metadata["model"] != "gpt-4.1" {
		t.Fatalf("expected model metadata, got %#v", metadata["model"])
	}
}
