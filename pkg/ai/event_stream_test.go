package ai

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestAssistantMessageEventStream_ResultFromDone(t *testing.T) {
	s := NewAssistantMessageEventStream(4)
	doneMsg := Message{Role: RoleAssistant, StopReason: StopReasonStop, Timestamp: 1}

	go func() {
		s.Push(AssistantMessageEvent{Type: EventStart})
		s.Push(AssistantMessageEvent{Type: EventDone, Message: doneMsg, Reason: StopReasonStop})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		_, err := s.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
	}

	result, err := s.Result()
	if err != nil {
		t.Fatalf("unexpected result error: %v", err)
	}
	if result.StopReason != StopReasonStop {
		t.Fatalf("expected stop reason stop, got %s", result.StopReason)
	}
}

func TestAssistantMessageEventStream_ResultFromError(t *testing.T) {
	s := NewAssistantMessageEventStream(2)
	errMsg := Message{Role: RoleAssistant, ErrorMessage: "boom", Timestamp: 2}

	go func() {
		s.Push(AssistantMessageEvent{Type: EventError, Error: errMsg})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	evt, err := s.Next(ctx)
	if err != nil {
		t.Fatalf("unexpected next error: %v", err)
	}
	if evt.Type != EventError {
		t.Fatalf("expected error event, got %s", evt.Type)
	}

	_, err = s.Next(ctx)
	if err != io.EOF {
		t.Fatalf("expected EOF after terminal event, got %v", err)
	}

	result, err := s.Result()
	if err != nil {
		t.Fatalf("unexpected result error: %v", err)
	}
	if result.ErrorMessage != "boom" {
		t.Fatalf("expected result from error event, got %#v", result)
	}
}
