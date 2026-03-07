package codex

import (
	"testing"

	"maunium.net/go/mautrix/id"
)

func TestCodexPendingQueueFIFO(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	cc := &CodexClient{
		activeRooms:     make(map[id.RoomID]bool),
		pendingMessages: make(map[id.RoomID]codexPendingQueue),
	}

	first := &codexPendingMessage{body: "first"}
	second := &codexPendingMessage{body: "second"}

	cc.queuePendingCodex(roomID, first)
	cc.queuePendingCodex(roomID, second)

	if got := cc.popPendingCodex(roomID); got != first {
		t.Fatalf("expected first pending message, got %#v", got)
	}
	if got := cc.popPendingCodex(roomID); got != second {
		t.Fatalf("expected second pending message, got %#v", got)
	}
	if got := cc.popPendingCodex(roomID); got != nil {
		t.Fatalf("expected queue to be empty, got %#v", got)
	}
}
