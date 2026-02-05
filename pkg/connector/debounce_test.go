package connector

import (
	"sync"
	"testing"
	"time"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestDebouncer_ImmediateFlush(t *testing.T) {
	var flushed [][]DebounceEntry
	var mu sync.Mutex

	debouncer := NewDebouncer(100, func(entries []DebounceEntry) {
		mu.Lock()
		flushed = append(flushed, entries)
		mu.Unlock()
	}, nil)

	entry := DebounceEntry{RawBody: "test"}

	// shouldDebounce=false should flush immediately
	debouncer.Enqueue("key1", entry, false)

	mu.Lock()
	if len(flushed) != 1 {
		t.Errorf("Expected 1 flush, got %d", len(flushed))
	}
	if len(flushed[0]) != 1 || flushed[0][0].RawBody != "test" {
		t.Error("Expected single entry with body 'test'")
	}
	mu.Unlock()
}

func TestDebouncer_EmptyKey(t *testing.T) {
	var flushed [][]DebounceEntry
	var mu sync.Mutex

	debouncer := NewDebouncer(100, func(entries []DebounceEntry) {
		mu.Lock()
		flushed = append(flushed, entries)
		mu.Unlock()
	}, nil)

	entry := DebounceEntry{RawBody: "test"}

	// Empty key should flush immediately
	debouncer.Enqueue("", entry, true)

	mu.Lock()
	if len(flushed) != 1 {
		t.Errorf("Expected 1 flush for empty key, got %d", len(flushed))
	}
	mu.Unlock()
}

func TestDebouncer_DelayedFlush(t *testing.T) {
	var flushed [][]DebounceEntry
	var mu sync.Mutex

	debouncer := NewDebouncer(50, func(entries []DebounceEntry) {
		mu.Lock()
		flushed = append(flushed, entries)
		mu.Unlock()
	}, nil)

	entry := DebounceEntry{RawBody: "test"}
	debouncer.Enqueue("key1", entry, true)

	// Should not be flushed immediately
	mu.Lock()
	if len(flushed) != 0 {
		t.Error("Should not flush immediately")
	}
	mu.Unlock()

	// Wait for debounce timer
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if len(flushed) != 1 {
		t.Errorf("Expected 1 flush after delay, got %d", len(flushed))
	}
	mu.Unlock()
}

func TestDebouncer_CombineMessages(t *testing.T) {
	var flushed [][]DebounceEntry
	var mu sync.Mutex

	debouncer := NewDebouncer(50, func(entries []DebounceEntry) {
		mu.Lock()
		flushed = append(flushed, entries)
		mu.Unlock()
	}, nil)

	// Send 3 messages rapidly
	debouncer.Enqueue("key1", DebounceEntry{RawBody: "msg1"}, true)
	debouncer.Enqueue("key1", DebounceEntry{RawBody: "msg2"}, true)
	debouncer.Enqueue("key1", DebounceEntry{RawBody: "msg3"}, true)

	// Wait for debounce timer
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if len(flushed) != 1 {
		t.Errorf("Expected 1 flush, got %d", len(flushed))
	}
	if len(flushed[0]) != 3 {
		t.Errorf("Expected 3 entries combined, got %d", len(flushed[0]))
	}
	mu.Unlock()
}

func TestDebouncer_SeparateKeys(t *testing.T) {
	var flushed [][]DebounceEntry
	var mu sync.Mutex

	debouncer := NewDebouncer(50, func(entries []DebounceEntry) {
		mu.Lock()
		flushed = append(flushed, entries)
		mu.Unlock()
	}, nil)

	// Send messages to different keys
	debouncer.Enqueue("key1", DebounceEntry{RawBody: "msg1"}, true)
	debouncer.Enqueue("key2", DebounceEntry{RawBody: "msg2"}, true)

	// Wait for debounce timer
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if len(flushed) != 2 {
		t.Errorf("Expected 2 flushes (one per key), got %d", len(flushed))
	}
	mu.Unlock()
}

func TestDebouncer_FlushKey(t *testing.T) {
	var flushed [][]DebounceEntry
	var mu sync.Mutex

	debouncer := NewDebouncer(1000, func(entries []DebounceEntry) { // Long delay
		mu.Lock()
		flushed = append(flushed, entries)
		mu.Unlock()
	}, nil)

	debouncer.Enqueue("key1", DebounceEntry{RawBody: "msg1"}, true)

	// Manually flush before timer
	debouncer.FlushKey("key1")

	mu.Lock()
	if len(flushed) != 1 {
		t.Errorf("Expected 1 flush after FlushKey, got %d", len(flushed))
	}
	mu.Unlock()
}

func TestDebouncer_FlushAll(t *testing.T) {
	var flushed [][]DebounceEntry
	var mu sync.Mutex

	debouncer := NewDebouncer(1000, func(entries []DebounceEntry) {
		mu.Lock()
		flushed = append(flushed, entries)
		mu.Unlock()
	}, nil)

	debouncer.Enqueue("key1", DebounceEntry{RawBody: "msg1"}, true)
	debouncer.Enqueue("key2", DebounceEntry{RawBody: "msg2"}, true)

	debouncer.FlushAll()

	mu.Lock()
	if len(flushed) != 2 {
		t.Errorf("Expected 2 flushes after FlushAll, got %d", len(flushed))
	}
	mu.Unlock()
}

func TestDebouncer_PendingCount(t *testing.T) {
	debouncer := NewDebouncer(1000, func(entries []DebounceEntry) {}, nil)

	if debouncer.PendingCount() != 0 {
		t.Error("Expected 0 pending initially")
	}

	debouncer.Enqueue("key1", DebounceEntry{RawBody: "msg1"}, true)
	debouncer.Enqueue("key2", DebounceEntry{RawBody: "msg2"}, true)

	if debouncer.PendingCount() != 2 {
		t.Errorf("Expected 2 pending, got %d", debouncer.PendingCount())
	}

	debouncer.FlushAll()

	if debouncer.PendingCount() != 0 {
		t.Errorf("Expected 0 pending after flush, got %d", debouncer.PendingCount())
	}
}

func TestShouldDebounce_TextMessage(t *testing.T) {
	evt := &event.Event{
		Type: event.EventMessage,
		Content: event.Content{
			Parsed: &event.MessageEventContent{
				MsgType: event.MsgText,
				Body:    "hello",
			},
		},
	}

	if !ShouldDebounce(evt, "hello") {
		t.Error("Text messages should be debounced")
	}
}

func TestShouldDebounce_MediaMessage(t *testing.T) {
	tests := []struct {
		name    string
		msgType event.MessageType
	}{
		{"image", event.MsgImage},
		{"video", event.MsgVideo},
		{"audio", event.MsgAudio},
		{"file", event.MsgFile},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := &event.Event{
				Type: event.EventMessage,
				Content: event.Content{
					Parsed: &event.MessageEventContent{
						MsgType: tt.msgType,
					},
				},
			}

			if ShouldDebounce(evt, "") {
				t.Errorf("%s messages should not be debounced", tt.name)
			}
		})
	}
}

func TestShouldDebounce_Command(t *testing.T) {
	evt := &event.Event{
		Type: event.EventMessage,
		Content: event.Content{
			Parsed: &event.MessageEventContent{
				MsgType: event.MsgText,
			},
		},
	}

	if ShouldDebounce(evt, "!ai model") {
		t.Error("Commands should not be debounced")
	}
	if ShouldDebounce(evt, "  !command") {
		t.Error("Commands with leading whitespace should not be debounced")
	}
	if ShouldDebounce(evt, "/status") {
		t.Error("Slash commands should not be debounced")
	}
	if ShouldDebounce(evt, "  /command") {
		t.Error("Slash commands with leading whitespace should not be debounced")
	}
}

func TestShouldDebounce_EmptyMessage(t *testing.T) {
	evt := &event.Event{
		Type: event.EventMessage,
		Content: event.Content{
			Parsed: &event.MessageEventContent{
				MsgType: event.MsgText,
			},
		},
	}

	if ShouldDebounce(evt, "") {
		t.Error("Empty messages should not be debounced")
	}
	if ShouldDebounce(evt, "   ") {
		t.Error("Whitespace-only messages should not be debounced")
	}
}

func TestBuildDebounceKey(t *testing.T) {
	roomID := id.RoomID("!room:server.com")
	sender := id.UserID("@user:server.com")

	key := BuildDebounceKey(roomID, sender)

	expected := "!room:server.com|@user:server.com"
	if key != expected {
		t.Errorf("Expected key %q, got %q", expected, key)
	}
}

func TestCombineDebounceEntries_Empty(t *testing.T) {
	body, count := CombineDebounceEntries(nil)
	if body != "" || count != 0 {
		t.Error("Empty entries should return empty body and 0 count")
	}
}

func TestCombineDebounceEntries_Single(t *testing.T) {
	entries := []DebounceEntry{{RawBody: "hello"}}
	body, count := CombineDebounceEntries(entries)
	if body != "hello" || count != 1 {
		t.Errorf("Expected 'hello' and 1, got %q and %d", body, count)
	}
}

func TestCombineDebounceEntries_Multiple(t *testing.T) {
	entries := []DebounceEntry{
		{RawBody: "line1"},
		{RawBody: "line2"},
		{RawBody: "line3"},
	}
	body, count := CombineDebounceEntries(entries)
	expected := "line1\nline2\nline3"
	if body != expected || count != 3 {
		t.Errorf("Expected %q and 3, got %q and %d", expected, body, count)
	}
}

func TestCombineDebounceEntries_SkipsEmpty(t *testing.T) {
	entries := []DebounceEntry{
		{RawBody: "line1"},
		{RawBody: ""},
		{RawBody: "line3"},
	}
	body, count := CombineDebounceEntries(entries)
	expected := "line1\nline3"
	if body != expected {
		t.Errorf("Expected %q, got %q", expected, body)
	}
	if count != 3 {
		t.Errorf("Expected count 3 (includes empty), got %d", count)
	}
}

func TestDebouncer_EnqueueWithDelay_CustomDelay(t *testing.T) {
	var flushed [][]DebounceEntry
	var mu sync.Mutex

	debouncer := NewDebouncer(1000, func(entries []DebounceEntry) { // Default 1s
		mu.Lock()
		flushed = append(flushed, entries)
		mu.Unlock()
	}, nil)

	entry := DebounceEntry{RawBody: "test"}

	// Use custom 50ms delay instead of default 1s
	debouncer.EnqueueWithDelay("key1", entry, true, 50)

	// Should not be flushed immediately
	mu.Lock()
	if len(flushed) != 0 {
		t.Error("Should not flush immediately")
	}
	mu.Unlock()

	// Wait for custom delay (less than default)
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if len(flushed) != 1 {
		t.Errorf("Expected 1 flush after custom delay, got %d", len(flushed))
	}
	mu.Unlock()
}

func TestDebouncer_EnqueueWithDelay_DisabledDelay(t *testing.T) {
	var flushed [][]DebounceEntry
	var mu sync.Mutex

	debouncer := NewDebouncer(1000, func(entries []DebounceEntry) {
		mu.Lock()
		flushed = append(flushed, entries)
		mu.Unlock()
	}, nil)

	entry := DebounceEntry{RawBody: "test"}

	// Negative delay = immediate (disabled)
	debouncer.EnqueueWithDelay("key1", entry, true, -1)

	mu.Lock()
	if len(flushed) != 1 {
		t.Errorf("Expected 1 immediate flush for disabled debounce, got %d", len(flushed))
	}
	mu.Unlock()
}

func TestDebouncer_EnqueueWithDelay_ZeroUsesDefault(t *testing.T) {
	var flushed [][]DebounceEntry
	var mu sync.Mutex

	debouncer := NewDebouncer(50, func(entries []DebounceEntry) { // Default 50ms
		mu.Lock()
		flushed = append(flushed, entries)
		mu.Unlock()
	}, nil)

	entry := DebounceEntry{RawBody: "test"}

	// Zero delay = use default
	debouncer.EnqueueWithDelay("key1", entry, true, 0)

	// Should not be flushed immediately
	mu.Lock()
	if len(flushed) != 0 {
		t.Error("Should not flush immediately")
	}
	mu.Unlock()

	// Wait for default delay
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if len(flushed) != 1 {
		t.Errorf("Expected 1 flush after default delay, got %d", len(flushed))
	}
	mu.Unlock()
}
