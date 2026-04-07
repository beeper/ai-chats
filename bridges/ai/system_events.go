package ai

import (
	"errors"
	"slices"
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
const systemEventsKeySeparator = "\x1f"

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

func enqueueSystemEvent(ownerKey string, sessionKey string, text string, contextKey string) {
	key, err := buildSystemEventsMapKey(ownerKey, sessionKey)
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

func drainSystemEventEntries(ownerKey string, sessionKey string) []SystemEvent {
	key, err := buildSystemEventsMapKey(ownerKey, sessionKey)
	if err != nil {
		return nil
	}
	systemEventsMu.Lock()
	entry := systemEvents[key]
	if entry == nil || len(entry.queue) == 0 {
		systemEventsMu.Unlock()
		return nil
	}
	out := slices.Clone(entry.queue)
	delete(systemEvents, key)
	systemEventsMu.Unlock()
	return out
}

func peekSystemEvents(ownerKey string, sessionKey string) []string {
	key, err := buildSystemEventsMapKey(ownerKey, sessionKey)
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

func hasSystemEvents(ownerKey string, sessionKey string) bool {
	key, err := buildSystemEventsMapKey(ownerKey, sessionKey)
	if err != nil {
		return false
	}
	systemEventsMu.Lock()
	entry := systemEvents[key]
	has := entry != nil && len(entry.queue) > 0
	systemEventsMu.Unlock()
	return has
}

func clearSystemEventsForSession(ownerKey string, sessionKey string) {
	key, err := buildSystemEventsMapKey(ownerKey, sessionKey)
	if err != nil {
		return
	}
	systemEventsMu.Lock()
	delete(systemEvents, key)
	systemEventsMu.Unlock()
}

func buildSystemEventsMapKey(ownerKey string, sessionKey string) (string, error) {
	owner := strings.TrimSpace(ownerKey)
	key, err := requireSessionKey(sessionKey)
	if err != nil {
		return "", err
	}
	if owner == "" {
		return "", errors.New("system events require an owner key")
	}
	return owner + systemEventsKeySeparator + key, nil
}

func splitSystemEventsMapKey(key string) (string, string, bool) {
	owner, sessionKey, ok := strings.Cut(strings.TrimSpace(key), systemEventsKeySeparator)
	if !ok || strings.TrimSpace(owner) == "" || strings.TrimSpace(sessionKey) == "" {
		return "", "", false
	}
	return owner, sessionKey, true
}
