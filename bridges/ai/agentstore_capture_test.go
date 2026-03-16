package ai

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/event"
)

func TestCaptureMatrixAPIWaitForMessagesCapturesAsync(t *testing.T) {
	capture := newCaptureMatrixAPI(nil)

	go func() {
		time.Sleep(20 * time.Millisecond)
		capture.captureContent(&event.Content{
			Parsed: &event.MessageEventContent{Body: "hello from async"},
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	capture.WaitForMessages(ctx, 200*time.Millisecond, 25*time.Millisecond)

	got := capture.Messages()
	if got != "hello from async" {
		t.Fatalf("expected captured async message, got %q", got)
	}
}

func TestCaptureMatrixAPIWaitForMessagesTimeout(t *testing.T) {
	capture := newCaptureMatrixAPI(nil)

	start := time.Now()
	capture.WaitForMessages(context.Background(), 40*time.Millisecond, 25*time.Millisecond)
	elapsed := time.Since(start)
	if elapsed < 35*time.Millisecond {
		t.Fatalf("expected wait close to timeout, got %v", elapsed)
	}
	if got := capture.Messages(); got != "" {
		t.Fatalf("expected no captured messages, got %q", got)
	}
}
