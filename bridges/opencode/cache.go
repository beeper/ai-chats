package opencode

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/beeper/agentremote/bridges/opencode/api"
)

const (
	openCodeBackfillRefreshInterval = 10 * time.Second
)

type messageCacheEntry struct {
	msg api.MessageWithParts
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

func (inst *openCodeInstance) listMessagesForBackfill(ctx context.Context, sessionID string, forward bool, count int) ([]api.MessageWithParts, error) {
	complete, lastRefresh, size := inst.cacheSnapshot(sessionID)
	_ = forward
	_ = count
	requireFull := !complete || size == 0 || time.Since(lastRefresh) > openCodeBackfillRefreshInterval
	if requireFull {
		_, err := inst.refreshMessages(ctx, sessionID, 0, true)
		if err != nil {
			return nil, err
		}
	}
	return inst.listCachedMessages(sessionID), nil
}

func (inst *openCodeInstance) refreshMessages(ctx context.Context, sessionID string, limit int, full bool) ([]api.MessageWithParts, error) {
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

func (inst *openCodeInstance) upsertMessages(sessionID string, msgs []api.MessageWithParts) {
	for _, msg := range msgs {
		inst.upsertMessage(sessionID, msg)
	}
}

func (inst *openCodeInstance) upsertMessage(sessionID string, msg api.MessageWithParts) {
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

func (inst *openCodeInstance) upsertPart(sessionID, messageID string, part api.Part) {
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
	entry.msg.Parts = slices.DeleteFunc(entry.msg.Parts, func(p api.Part) bool {
		return p.ID == partID
	})
	cache.messages[messageID] = entry
	cache.mu.Unlock()
}

func (inst *openCodeInstance) listCachedMessages(sessionID string) []api.MessageWithParts {
	cache := inst.ensureMessageCache(sessionID)
	cache.mu.Lock()
	if cache.dirty {
		cache.order = cache.order[:0]
		for id := range cache.messages {
			cache.order = append(cache.order, id)
		}
		slices.SortStableFunc(cache.order, func(a, b string) int {
			left := cache.messages[a]
			right := cache.messages[b]
			if c := left.ts.Compare(right.ts); c != 0 {
				return c
			}
			return cmp.Compare(a, b)
		})
		cache.dirty = false
	}
	out := make([]api.MessageWithParts, 0, len(cache.order))
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

func (inst *openCodeInstance) enqueueMessage(sessionID string, item *queuedUserMessage) *queuedUserMessage {
	if inst == nil || sessionID == "" || item == nil {
		return nil
	}
	inst.queueMu.Lock()
	defer inst.queueMu.Unlock()
	if inst.sendQueue == nil {
		inst.sendQueue = make(map[string]*openCodeSessionQueue)
	}
	queue := inst.sendQueue[sessionID]
	if queue == nil {
		queue = &openCodeSessionQueue{}
		inst.sendQueue[sessionID] = queue
	}
	queue.items = append(queue.items, item)
	if queue.active {
		return nil
	}
	queue.active = true
	next := queue.items[0]
	queue.items = queue.items[1:]
	return next
}

func (inst *openCodeInstance) requeueMessageFront(sessionID string, item *queuedUserMessage) {
	if inst == nil || sessionID == "" || item == nil {
		return
	}
	inst.queueMu.Lock()
	defer inst.queueMu.Unlock()
	if inst.sendQueue == nil {
		inst.sendQueue = make(map[string]*openCodeSessionQueue)
	}
	queue := inst.sendQueue[sessionID]
	if queue == nil {
		queue = &openCodeSessionQueue{}
		inst.sendQueue[sessionID] = queue
	}
	queue.items = append([]*queuedUserMessage{item}, queue.items...)
}

func (inst *openCodeInstance) markSessionIdle(sessionID string) *queuedUserMessage {
	if inst == nil || sessionID == "" {
		return nil
	}
	inst.queueMu.Lock()
	defer inst.queueMu.Unlock()
	queue := inst.sendQueue[sessionID]
	if queue == nil {
		return nil
	}
	if len(queue.items) == 0 {
		queue.active = false
		delete(inst.sendQueue, sessionID)
		return nil
	}
	next := queue.items[0]
	queue.items = queue.items[1:]
	queue.active = true
	return next
}

func (inst *openCodeInstance) releaseActiveSession(sessionID string) {
	if inst == nil || sessionID == "" {
		return
	}
	inst.queueMu.Lock()
	defer inst.queueMu.Unlock()
	queue := inst.sendQueue[sessionID]
	if queue == nil {
		return
	}
	queue.active = false
	if len(queue.items) == 0 {
		delete(inst.sendQueue, sessionID)
	}
}
