package opencodebridge

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/beeper/ai-bridge/pkg/opencode"
)

const (
	openCodeBackfillRefreshInterval = 10 * time.Second
	openCodeBackfillRefreshLimit    = 200
)

type messageCacheEntry struct {
	msg opencode.MessageWithParts
	ts  time.Time
}

type openCodeMessageCache struct {
	mu          sync.Mutex
	messages    map[string]messageCacheEntry
	order       []string
	complete    bool
	dirty       bool
	lastRefresh time.Time
}

func (inst *openCodeInstance) ensureMessageCache(sessionID string) *openCodeMessageCache {
	inst.cacheMu.Lock()
	defer inst.cacheMu.Unlock()
	if inst.messageCache == nil {
		inst.messageCache = make(map[string]*openCodeMessageCache)
	}
	cache := inst.messageCache[sessionID]
	if cache == nil {
		cache = &openCodeMessageCache{messages: make(map[string]messageCacheEntry), dirty: true}
		inst.messageCache[sessionID] = cache
	}
	return cache
}

func (inst *openCodeInstance) cacheSnapshot(sessionID string) (bool, time.Time, int) {
	cache := inst.ensureMessageCache(sessionID)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.complete, cache.lastRefresh, len(cache.messages)
}

func (inst *openCodeInstance) listMessagesForBackfill(ctx context.Context, sessionID string, forward bool, count int) ([]opencode.MessageWithParts, error) {
	complete, lastRefresh, size := inst.cacheSnapshot(sessionID)
	requireFull := !forward && !complete
	refreshLimit := 0
	if forward {
		refreshLimit = openCodeBackfillRefreshLimit
		if count > refreshLimit {
			refreshLimit = count
		}
		if size == 0 {
			refreshLimit = max(refreshLimit, openCodeBackfillRefreshLimit)
		}
	}
	if requireFull || (refreshLimit > 0 && time.Since(lastRefresh) > openCodeBackfillRefreshInterval) || size == 0 {
		limit := 0
		if !requireFull {
			limit = refreshLimit
		}
		_, err := inst.refreshMessages(ctx, sessionID, limit, requireFull)
		if err != nil {
			return nil, err
		}
	}
	return inst.listCachedMessages(sessionID), nil
}

func (inst *openCodeInstance) refreshMessages(ctx context.Context, sessionID string, limit int, full bool) ([]opencode.MessageWithParts, error) {
	msgs, err := inst.client.ListMessages(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}
	cache := inst.ensureMessageCache(sessionID)
	cache.mu.Lock()
	cache.lastRefresh = time.Now()
	if full {
		cache.complete = true
	}
	cache.mu.Unlock()
	inst.upsertMessages(sessionID, msgs)
	return inst.listCachedMessages(sessionID), nil
}

func (inst *openCodeInstance) upsertMessages(sessionID string, msgs []opencode.MessageWithParts) {
	for _, msg := range msgs {
		inst.upsertMessage(sessionID, msg)
	}
}

func (inst *openCodeInstance) upsertMessage(sessionID string, msg opencode.MessageWithParts) {
	if sessionID == "" {
		sessionID = msg.Info.SessionID
	}
	if sessionID == "" || msg.Info.ID == "" {
		return
	}
	for i := range msg.Parts {
		if msg.Parts[i].MessageID == "" {
			msg.Parts[i].MessageID = msg.Info.ID
		}
		if msg.Parts[i].SessionID == "" {
			msg.Parts[i].SessionID = sessionID
		}
	}
	cache := inst.ensureMessageCache(sessionID)
	entry := messageCacheEntry{msg: msg, ts: openCodeMessageTime(msg)}
	cache.mu.Lock()
	cache.messages[msg.Info.ID] = entry
	cache.dirty = true
	cache.mu.Unlock()
}

func (inst *openCodeInstance) upsertPart(sessionID, messageID string, part opencode.Part) {
	if sessionID == "" || messageID == "" || part.ID == "" {
		return
	}
	cache := inst.ensureMessageCache(sessionID)
	cache.mu.Lock()
	entry, ok := cache.messages[messageID]
	if !ok {
		cache.mu.Unlock()
		return
	}
	updated := false
	for i := range entry.msg.Parts {
		if entry.msg.Parts[i].ID == part.ID {
			entry.msg.Parts[i] = part
			updated = true
			break
		}
	}
	if !updated {
		entry.msg.Parts = append(entry.msg.Parts, part)
	}
	cache.messages[messageID] = entry
	cache.mu.Unlock()
}

func (inst *openCodeInstance) removeCachedMessage(sessionID, messageID string) {
	if sessionID == "" || messageID == "" {
		return
	}
	cache := inst.ensureMessageCache(sessionID)
	cache.mu.Lock()
	delete(cache.messages, messageID)
	cache.dirty = true
	cache.mu.Unlock()
}

func (inst *openCodeInstance) removeCachedPart(sessionID, messageID, partID string) {
	if sessionID == "" || messageID == "" || partID == "" {
		return
	}
	cache := inst.ensureMessageCache(sessionID)
	cache.mu.Lock()
	entry, ok := cache.messages[messageID]
	if !ok {
		cache.mu.Unlock()
		return
	}
	parts := entry.msg.Parts[:0]
	for _, part := range entry.msg.Parts {
		if part.ID == partID {
			continue
		}
		parts = append(parts, part)
	}
	entry.msg.Parts = parts
	cache.messages[messageID] = entry
	cache.mu.Unlock()
}

func (inst *openCodeInstance) listCachedMessages(sessionID string) []opencode.MessageWithParts {
	cache := inst.ensureMessageCache(sessionID)
	cache.mu.Lock()
	if cache.dirty {
		cache.order = cache.order[:0]
		for id := range cache.messages {
			cache.order = append(cache.order, id)
		}
		sort.SliceStable(cache.order, func(i, j int) bool {
			left := cache.messages[cache.order[i]]
			right := cache.messages[cache.order[j]]
			if left.ts.Equal(right.ts) {
				return cache.order[i] < cache.order[j]
			}
			return left.ts.Before(right.ts)
		})
		cache.dirty = false
	}
	out := make([]opencode.MessageWithParts, 0, len(cache.order))
	for _, id := range cache.order {
		entry, ok := cache.messages[id]
		if !ok {
			continue
		}
		out = append(out, entry.msg)
	}
	cache.mu.Unlock()
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
