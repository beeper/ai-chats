package sdk

import (
	"context"
	"testing"
	"time"
)

func TestTurnManagerSerializesPerKey(t *testing.T) {
	tm := NewTurnManager(&TurnConfig{OneAtATime: true})
	release, err := tm.Acquire(context.Background(), "room-1")
	if err != nil {
		t.Fatalf("unexpected acquire error: %v", err)
	}
	defer release()

	done := make(chan error, 1)
	go func() {
		_, err := tm.Acquire(context.Background(), "room-1")
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("second acquire should block until release, got %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	release()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("unexpected acquire error after release: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("second acquire did not proceed after release")
	}
}
