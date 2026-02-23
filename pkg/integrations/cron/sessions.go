package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	croncore "github.com/beeper/ai-bridge/pkg/cron"
)

type SessionEntry struct {
	SessionID        string `json:"sessionId,omitempty"`
	UpdatedAt        int64  `json:"updatedAt,omitempty"`
	Model            string `json:"model,omitempty"`
	PromptTokens     int64  `json:"promptTokens,omitempty"`
	CompletionTokens int64  `json:"completionTokens,omitempty"`
	TotalTokens      int64  `json:"totalTokens,omitempty"`
}

type SessionStore struct {
	Sessions map[string]SessionEntry `json:"sessions"`
}

const SessionStorePath = "cron/sessions.json"

var sessionStoreMu sync.Mutex

type SessionStoreBackend interface {
	Read(ctx context.Context, key string) ([]byte, bool, error)
	Write(ctx context.Context, key string, data []byte) error
}

func CronSessionKey(agentID, jobID string, normalizeAgentID func(string) string) string {
	id := strings.TrimSpace(agentID)
	if normalizeAgentID != nil {
		id = normalizeAgentID(id)
	}
	if id == "" {
		id = "main"
	}
	job := strings.TrimSpace(jobID)
	if job == "" {
		job = "job"
	}
	return fmt.Sprintf("agent:%s:cron:%s", id, job)
}

func LoadSessionStore(ctx context.Context, backend SessionStoreBackend, log croncore.Logger) (SessionStore, error) {
	empty := SessionStore{Sessions: map[string]SessionEntry{}}
	if backend == nil {
		return empty, nil
	}
	data, found, err := backend.Read(ctx, SessionStorePath)
	if err != nil || !found {
		return empty, nil
	}
	var parsed SessionStore
	if err := json.Unmarshal(data, &parsed); err != nil {
		if log != nil {
			log.Warn("cron session store: JSON unmarshal failed, returning empty store", map[string]any{"error": err.Error()})
		}
		return empty, nil
	}
	if parsed.Sessions == nil {
		parsed.Sessions = map[string]SessionEntry{}
	}
	return parsed, nil
}

func SaveSessionStore(ctx context.Context, backend SessionStoreBackend, store SessionStore) error {
	if store.Sessions == nil {
		store.Sessions = map[string]SessionEntry{}
	}
	blob, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	if backend == nil {
		return nil
	}
	return backend.Write(ctx, SessionStorePath, blob)
}

func UpdateSessionEntry(
	ctx context.Context,
	backend SessionStoreBackend,
	log croncore.Logger,
	sessionKey string,
	updater func(entry SessionEntry) SessionEntry,
) {
	if updater == nil {
		return
	}
	sessionStoreMu.Lock()
	defer sessionStoreMu.Unlock()
	store, err := LoadSessionStore(ctx, backend, log)
	if err != nil {
		if log != nil {
			log.Warn("cron session store: load failed", map[string]any{"session_key": sessionKey, "error": err.Error()})
		}
		return
	}
	entry := store.Sessions[sessionKey]
	entry = updater(entry)
	store.Sessions[sessionKey] = entry
	if err := SaveSessionStore(ctx, backend, store); err != nil && log != nil {
		log.Warn("cron session store: save failed", map[string]any{"session_key": sessionKey, "error": err.Error()})
	}
}
