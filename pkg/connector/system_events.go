//lint:file-ignore U1000 System event buffering is staged for future use.
package connector

import (
	"errors"
	"strings"
	"sync"
	"time"
)

type SystemEvent struct {
	Text string
	TS   int64
}

type systemEventQueue struct {
	queue          []SystemEvent
	lastText       string
	lastContextKey string
}

var (
	systemEventsMu sync.Mutex
	systemEvents   = make(map[string]*systemEventQueue)
)

const maxSystemEvents = 20

func requireSessionKey(key string) (string, error) {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return "", errors.New("system events require a session key")
	}
	return trimmed, nil
}

func normalizeContextKey(key string) string {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return ""
	}
	return strings.ToLower(trimmed)
}

func enqueueSystemEvent(sessionKey string, text string, contextKey string) {
	key, err := requireSessionKey(sessionKey)
	if err != nil {
		return
	}
	cleaned := strings.TrimSpace(text)
	if cleaned == "" {
		return
	}
	systemEventsMu.Lock()
	entry := systemEvents[key]
	if entry == nil {
		entry = &systemEventQueue{}
		systemEvents[key] = entry
	}
	entry.lastContextKey = normalizeContextKey(contextKey)
	if entry.lastText == cleaned {
		systemEventsMu.Unlock()
		return
	}
	entry.lastText = cleaned
	entry.queue = append(entry.queue, SystemEvent{Text: cleaned, TS: time.Now().UnixMilli()})
	if len(entry.queue) > maxSystemEvents {
		entry.queue = entry.queue[len(entry.queue)-maxSystemEvents:]
	}
	systemEventsMu.Unlock()
}

func drainSystemEventEntries(sessionKey string) []SystemEvent {
	key, err := requireSessionKey(sessionKey)
	if err != nil {
		return nil
	}
	systemEventsMu.Lock()
	entry := systemEvents[key]
	if entry == nil || len(entry.queue) == 0 {
		systemEventsMu.Unlock()
		return nil
	}
	out := make([]SystemEvent, len(entry.queue))
	copy(out, entry.queue)
	delete(systemEvents, key)
	systemEventsMu.Unlock()
	return out
}

func peekSystemEvents(sessionKey string) []string {
	key, err := requireSessionKey(sessionKey)
	if err != nil {
		return nil
	}
	systemEventsMu.Lock()
	entry := systemEvents[key]
	if entry == nil || len(entry.queue) == 0 {
		systemEventsMu.Unlock()
		return nil
	}
	out := make([]string, 0, len(entry.queue))
	for _, evt := range entry.queue {
		out = append(out, evt.Text)
	}
	systemEventsMu.Unlock()
	return out
}

func hasSystemEvents(sessionKey string) bool {
	key, err := requireSessionKey(sessionKey)
	if err != nil {
		return false
	}
	systemEventsMu.Lock()
	entry := systemEvents[key]
	has := entry != nil && len(entry.queue) > 0
	systemEventsMu.Unlock()
	return has
}
