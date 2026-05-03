package connector

import (
	"context"
	"errors"
	"testing"

	"github.com/openai/openai-go/v3/responses"
	"github.com/rs/zerolog"

	"github.com/beeper/ai-chats/pkg/shared/aihelpers"
	"github.com/beeper/ai-chats/pkg/shared/streamui"
)

func TestFinalizeStreamingStepErrorFinalizesContextLength(t *testing.T) {
	state := newTestStreamingStateWithTurn()
	state.turn.SetSuppressSend(true)

	client := &AIClient{}
	stepErr := errors.New("This model's maximum context length is 100 tokens. However, your messages resulted in 120 tokens.")

	cle, err := client.finalizeStreamingStepError(context.Background(), nil, state, nil, true, context.Background(), stepErr, func(error) {})
	if cle == nil {
		t.Fatal("expected context-length error")
	}
	if err != nil {
		t.Fatalf("expected context-length finalization to preserve retry path, got %v", err)
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

	meta := (&AIClient{}).buildStreamingMessageMetadata(state, nil, aihelpers.TurnData{})
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

func TestProcessResponseStreamEventEmitsMetadataForCompleted(t *testing.T) {
	state := newTestStreamingStateWithTurn()
	oc := &AIClient{}

	state.writer().Start(context.Background(), map[string]any{
		"turn_id": state.turn.ID(),
	})

	rsc := &responseStreamContext{
		base: &streamProviderBase{
			oc:    oc,
			log:   zerolog.Nop(),
			state: state,
		},
	}
	_, _, err := oc.processResponseStreamEvent(context.Background(), rsc, responses.ResponseStreamEventUnion{
		Type: "response.completed",
		Response: responses.Response{
			ID:     "resp_123",
			Status: "completed",
			Model:  "gpt-4.1",
		},
	}, false)
	if err != nil {
		t.Fatalf("unexpected completed error: %v", err)
	}

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

func TestBuildStreamUIMessageCanonicalizesTerminalResponseStatus(t *testing.T) {
	state := newTestStreamingStateWithTurn()
	oc := &AIClient{}

	state.writer().Start(context.Background(), map[string]any{
		"turn_id": state.turn.ID(),
	})

	state.responseID = "resp_123"
	state.responseStatus = "in_progress"
	state.completedAtMs = 123
	state.finishReason = "stop"

	message := oc.buildStreamUIMessage(state, nil, nil)
	metadata, _ := message["metadata"].(map[string]any)
	if metadata["response_status"] != "completed" {
		t.Fatalf("expected canonical completed response_status, got %#v", metadata["response_status"])
	}
	if metadata["response_id"] != "resp_123" {
		t.Fatalf("expected response_id metadata, got %#v", metadata["response_id"])
	}
}

func TestProcessResponseStreamEventUpdatesCompletedResponseStatus(t *testing.T) {
	state := newTestStreamingStateWithTurn()
	oc := &AIClient{}

	state.turn.SetSuppressSend(true)
	state.writer().Start(context.Background(), map[string]any{
		"turn_id": state.turn.ID(),
	})

	rsc := &responseStreamContext{
		base: &streamProviderBase{
			oc:    oc,
			log:   zerolog.Nop(),
			state: state,
		},
	}

	_, _, err := oc.processResponseStreamEvent(context.Background(), rsc, responses.ResponseStreamEventUnion{
		Type: "response.in_progress",
		Response: responses.Response{
			ID:     "resp_123",
			Status: "in_progress",
		},
	}, false)
	if err != nil {
		t.Fatalf("unexpected in_progress error: %v", err)
	}

	_, _, err = oc.processResponseStreamEvent(context.Background(), rsc, responses.ResponseStreamEventUnion{
		Type: "response.completed",
		Response: responses.Response{
			ID:     "resp_123",
			Status: "completed",
		},
	}, false)
	if err != nil {
		t.Fatalf("unexpected completed error: %v", err)
	}

	if state.responseStatus != "completed" {
		t.Fatalf("expected completed responseStatus, got %q", state.responseStatus)
	}
	message := streamui.SnapshotUIMessage(state.turn.UIState())
	metadata, _ := message["metadata"].(map[string]any)
	if metadata["response_status"] != "completed" {
		t.Fatalf("expected writer metadata to be completed, got %#v", metadata["response_status"])
	}
}

func TestProcessResponseStreamEventCompletedSignalsLoopStop(t *testing.T) {
	state := newTestStreamingStateWithTurn()
	oc := &AIClient{}

	rsc := &responseStreamContext{
		base: &streamProviderBase{
			oc:    oc,
			log:   zerolog.Nop(),
			state: state,
		},
	}

	done, cle, err := oc.processResponseStreamEvent(context.Background(), rsc, responses.ResponseStreamEventUnion{
		Type: "response.completed",
		Response: responses.Response{
			ID:     "resp_done",
			Status: "completed",
		},
	}, false)
	if !done {
		t.Fatal("expected completed response event to stop the stream loop")
	}
	if cle != nil {
		t.Fatalf("did not expect context-length error, got %#v", cle)
	}
	if err != nil {
		t.Fatalf("did not expect error, got %v", err)
	}
}

func TestResponsesTurnAdapterFinalizeStreamingTurnDoesNotSkipTerminalLifecycle(t *testing.T) {
	state := newTestStreamingStateWithTurn()
	state.turn.SetSuppressSend(true)
	state.writer().TextDelta(context.Background(), "done")
	state.completedAtMs = 123
	state.finishReason = "stop"

	adapter := &responsesTurnAdapter{
		streamProviderBase: streamProviderBase{
			oc:    &AIClient{},
			log:   zerolog.Nop(),
			state: state,
		},
	}

	adapter.FinalizeStreamingTurn(context.Background())

	if !state.isFinalized() {
		t.Fatal("expected finalization to mark terminal response state")
	}

	message := streamui.SnapshotUIMessage(state.turn.UIState())
	metadata, _ := message["metadata"].(map[string]any)
	if metadata["finish_reason"] != "stop" {
		t.Fatalf("expected finalized UI message finish_reason stop, got %#v", metadata["finish_reason"])
	}
}

func TestProcessResponseStreamEventFailedFinalizesAsError(t *testing.T) {
	state := newTestStreamingStateWithTurn()
	state.turn.SetSuppressSend(true)
	state.writer().TextDelta(context.Background(), "hello")
	oc := &AIClient{}

	rsc := &responseStreamContext{
		base: &streamProviderBase{
			oc:    oc,
			log:   zerolog.Nop(),
			state: state,
		},
	}

	done, cle, err := oc.processResponseStreamEvent(context.Background(), rsc, responses.ResponseStreamEventUnion{
		Type: "response.failed",
		Response: responses.Response{
			ID:     "resp_failed",
			Status: "failed",
			Error: responses.ResponseError{
				Message: "boom",
			},
		},
	}, false)
	if !done {
		t.Fatal("expected failed response event to stop the stream loop")
	}
	if cle != nil {
		t.Fatalf("did not expect context-length error, got %#v", cle)
	}
	if err == nil {
		t.Fatal("expected failed response event to return an error")
	}
	if !state.isFinalized() {
		t.Fatal("expected failed response event to finalize the turn")
	}
	message := streamui.SnapshotUIMessage(state.turn.UIState())
	metadata, _ := message["metadata"].(map[string]any)
	if metadata["finish_reason"] != "error" {
		t.Fatalf("expected error finish_reason, got %#v", metadata["finish_reason"])
	}
}
