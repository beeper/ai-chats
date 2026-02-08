package connector

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// DefaultDebounceMs is the default debounce delay in milliseconds.
const DefaultDebounceMs = 0

// DebounceEntry represents a buffered message waiting to be processed.
type DebounceEntry struct {
	Event        *event.Event
	Portal       *bridgev2.Portal
	Meta         *PortalMetadata
	RawBody      string
	SenderName   string
	RoomName     string
	IsGroup      bool
	WasMentioned bool
	AckEventID   id.EventID // Track ack reaction for removal after flush
	PendingSent  bool       // Whether a pending status was already sent for this event
}

// DebounceBuffer holds pending messages for a key.
type DebounceBuffer struct {
	entries []DebounceEntry
	timer   *time.Timer
}

// Debouncer buffers rapid messages and combines them.
// Based on clawdbot's inbound-debounce.ts implementation.
type Debouncer struct {
	mu      sync.Mutex
	buffers map[string]*DebounceBuffer
	delayMs int
	onFlush func(entries []DebounceEntry)
	onError func(err error, entries []DebounceEntry)
	log     zerolog.Logger
}

// NewDebouncer creates a new debouncer with the given delay and callbacks.
func NewDebouncer(delayMs int, onFlush func([]DebounceEntry), onError func(error, []DebounceEntry)) *Debouncer {
	if delayMs < 0 {
		delayMs = 0
	}
	return &Debouncer{
		buffers: make(map[string]*DebounceBuffer),
		delayMs: delayMs,
		onFlush: onFlush,
		onError: onError,
	}
}

// NewDebouncerWithLogger creates a new debouncer with logging support.
func NewDebouncerWithLogger(delayMs int, onFlush func([]DebounceEntry), onError func(error, []DebounceEntry), log zerolog.Logger) *Debouncer {
	d := NewDebouncer(delayMs, onFlush, onError)
	d.log = log
	return d
}

// BuildDebounceKey creates a key for debouncing: room+sender.
func BuildDebounceKey(roomID id.RoomID, sender id.UserID) string {
	return fmt.Sprintf("%s|%s", roomID, sender)
}

// Enqueue adds a message to the debounce buffer.
// If shouldDebounce is false, the message is processed immediately.
func (d *Debouncer) Enqueue(key string, entry DebounceEntry, shouldDebounce bool) {
	d.EnqueueWithDelay(key, entry, shouldDebounce, 0)
}

// EnqueueWithDelay adds a message with a custom debounce delay.
// delayMs: 0 = use default, -1 = immediate (no debounce), >0 = custom delay
func (d *Debouncer) EnqueueWithDelay(key string, entry DebounceEntry, shouldDebounce bool, delayMs int) {
	// Use default delay if not specified
	if delayMs == 0 {
		delayMs = d.delayMs
	}
	if key == "" || !shouldDebounce || delayMs <= 0 {
		// Flush pending buffer for this key before immediate processing.
		if key != "" {
			d.flush(key)
		}
		d.onFlush([]DebounceEntry{entry})
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	buf, exists := d.buffers[key]
	if exists {
		// Add to existing buffer, reset timer with the new delay
		buf.entries = append(buf.entries, entry)
		buf.timer.Reset(time.Duration(delayMs) * time.Millisecond)
		d.log.Debug().Str("key", key).Int("buffered", len(buf.entries)).Int("delay_ms", delayMs).Msg("Debounce buffering message")
	} else {
		// Create new buffer with timer
		buf = &DebounceBuffer{entries: []DebounceEntry{entry}}
		buf.timer = time.AfterFunc(time.Duration(delayMs)*time.Millisecond, func() {
			d.flush(key)
		})
		d.buffers[key] = buf
		d.log.Debug().Str("key", key).Int("delay_ms", delayMs).Msg("Debounce new buffer created")
	}
}

// flush processes all entries for a key and removes the buffer.
func (d *Debouncer) flush(key string) {
	d.mu.Lock()
	buf, exists := d.buffers[key]
	if !exists || len(buf.entries) == 0 {
		d.mu.Unlock()
		return
	}
	entries := buf.entries
	if buf.timer != nil {
		buf.timer.Stop()
	}
	delete(d.buffers, key)
	d.mu.Unlock()

	d.log.Debug().Str("key", key).Int("entries", len(entries)).Msg("Debounce flushing")
	d.onFlush(entries)
}

// FlushKey immediately flushes the buffer for a key (e.g., when media arrives).
func (d *Debouncer) FlushKey(key string) {
	d.flush(key)
}

// FlushAll flushes all pending buffers (e.g., on shutdown).
func (d *Debouncer) FlushAll() {
	d.mu.Lock()
	keys := make([]string, 0, len(d.buffers))
	for k := range d.buffers {
		keys = append(keys, k)
	}
	d.mu.Unlock()

	for _, key := range keys {
		d.flush(key)
	}
}

// PendingCount returns the number of keys with pending buffers.
func (d *Debouncer) PendingCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.buffers)
}

// ShouldDebounce returns false for messages that shouldn't be debounced.
// Media, commands, and empty messages are processed immediately.
func ShouldDebounce(evt *event.Event, body string) bool {
	// Don't debounce non-message events
	if evt.Type != event.EventMessage {
		return false
	}

	content, ok := evt.Content.Parsed.(*event.MessageEventContent)
	if !ok {
		return true
	}

	// Don't debounce media messages
	switch content.MsgType {
	case event.MsgImage, event.MsgVideo, event.MsgAudio, event.MsgFile:
		return false
	}

	// Don't debounce commands (starting with !)
	trimmed := strings.TrimSpace(body)
	if strings.HasPrefix(trimmed, "!") {
		return false
	}
	if isAbortTrigger(trimmed) {
		return false
	}

	// Don't debounce empty messages
	if trimmed == "" {
		return false
	}

	return true
}

// CombineDebounceEntries combines multiple entries into a single body.
// Returns the combined body and the count of combined messages.
func CombineDebounceEntries(entries []DebounceEntry) (string, int) {
	if len(entries) == 0 {
		return "", 0
	}
	if len(entries) == 1 {
		return entries[0].RawBody, 1
	}

	var bodies []string
	for _, e := range entries {
		if e.RawBody != "" {
			bodies = append(bodies, e.RawBody)
		}
	}
	return strings.Join(bodies, "\n"), len(entries)
}
