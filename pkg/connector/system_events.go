//lint:file-ignore U1000 System event buffering is staged for future use.
package connector

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/beeper/ai-bridge/pkg/cron"
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

// --- Persistence ---

const systemEventsStorePath = "sessions/system_events.json"

type persistedEvent struct {
	Text string `json:"text"`
	TS   int64  `json:"ts"`
}

type persistedQueue struct {
	Events   []persistedEvent `json:"events"`
	LastText string           `json:"lastText,omitempty"`
}

type persistedSystemEvents struct {
	Queues map[string]*persistedQueue `json:"queues"`
}

func snapshotSystemEvents() persistedSystemEvents {
	systemEventsMu.Lock()
	snap := persistedSystemEvents{Queues: make(map[string]*persistedQueue, len(systemEvents))}
	for key, entry := range systemEvents {
		if entry == nil || len(entry.queue) == 0 {
			continue
		}
		events := make([]persistedEvent, len(entry.queue))
		for i, evt := range entry.queue {
			events[i] = persistedEvent{Text: evt.Text, TS: evt.TS}
		}
		snap.Queues[key] = &persistedQueue{Events: events, LastText: entry.lastText}
	}
	systemEventsMu.Unlock()
	return snap
}

func persistSystemEventsSnapshot(backend cron.StoreBackend, log *zerolog.Logger) {
	if backend == nil {
		return
	}
	snap := snapshotSystemEvents()
	data, err := json.Marshal(snap)
	if err != nil {
		if log != nil {
			log.Warn().Err(err).Msg("system events: marshal failed during persist")
		}
		return
	}
	if err := backend.Write(context.Background(), systemEventsStorePath, data); err != nil {
		if log != nil {
			log.Warn().Err(err).Msg("system events: write failed during persist")
		}
	}
}

func restoreSystemEventsFromDisk(backend cron.StoreBackend, log *zerolog.Logger) {
	if backend == nil {
		return
	}
	data, found, err := backend.Read(context.Background(), systemEventsStorePath)
	if err != nil {
		if log != nil {
			log.Warn().Err(err).Msg("system events: read failed during restore")
		}
		return
	}
	if !found || len(data) == 0 {
		return
	}
	var snap persistedSystemEvents
	if err := json.Unmarshal(data, &snap); err != nil {
		if log != nil {
			log.Warn().Err(err).Msg("system events: unmarshal failed during restore")
		}
		return
	}
	systemEventsMu.Lock()
	for key, pq := range snap.Queues {
		if pq == nil || len(pq.Events) == 0 {
			continue
		}
		existing := systemEvents[key]
		if existing != nil && len(existing.queue) > 0 {
			continue // don't overwrite events already in memory
		}
		events := make([]SystemEvent, len(pq.Events))
		for i, pe := range pq.Events {
			events[i] = SystemEvent{Text: pe.Text, TS: pe.TS}
		}
		systemEvents[key] = &systemEventQueue{
			queue:    events,
			lastText: pq.LastText,
		}
	}
	systemEventsMu.Unlock()
}
