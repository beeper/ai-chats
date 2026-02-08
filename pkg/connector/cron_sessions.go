package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type cronSessionEntry struct {
	SessionID        string `json:"sessionId,omitempty"`
	UpdatedAt        int64  `json:"updatedAt,omitempty"`
	Model            string `json:"model,omitempty"`
	PromptTokens     int64  `json:"promptTokens,omitempty"`
	CompletionTokens int64  `json:"completionTokens,omitempty"`
	TotalTokens      int64  `json:"totalTokens,omitempty"`
}

type cronSessionStore struct {
	Sessions map[string]cronSessionEntry `json:"sessions"`
}

const cronSessionStorePath = "cron/sessions.json"

func cronSessionKey(agentID, jobID string) string {
	id := normalizeAgentID(agentID)
	if id == "" {
		id = "main"
	}
	job := strings.TrimSpace(jobID)
	if job == "" {
		job = "job"
	}
	return fmt.Sprintf("agent:%s:cron:%s", id, job)
}

func (oc *AIClient) loadCronSessionStore(ctx context.Context) (cronSessionStore, error) {
	backend := oc.bridgeStateBackend()
	if backend == nil {
		return cronSessionStore{Sessions: map[string]cronSessionEntry{}}, nil
	}
	data, found, err := backend.Read(ctx, cronSessionStorePath)
	if err != nil || !found {
		return cronSessionStore{Sessions: map[string]cronSessionEntry{}}, nil
	}
	var parsed cronSessionStore
	if err := json.Unmarshal(data, &parsed); err != nil {
		return cronSessionStore{Sessions: map[string]cronSessionEntry{}}, nil
	}
	if parsed.Sessions == nil {
		parsed.Sessions = map[string]cronSessionEntry{}
	}
	return parsed, nil
}

func (oc *AIClient) saveCronSessionStore(ctx context.Context, store cronSessionStore) error {
	if store.Sessions == nil {
		store.Sessions = map[string]cronSessionEntry{}
	}
	blob, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	backend := oc.bridgeStateBackend()
	if backend == nil {
		return nil
	}
	return backend.Write(ctx, cronSessionStorePath, blob)
}

func (oc *AIClient) updateCronSessionEntry(ctx context.Context, sessionKey string, updater func(entry cronSessionEntry) cronSessionEntry) {
	if oc == nil {
		return
	}
	store, err := oc.loadCronSessionStore(ctx)
	if err != nil {
		return
	}
	entry := store.Sessions[sessionKey]
	entry = updater(entry)
	store.Sessions[sessionKey] = entry
	_ = oc.saveCronSessionStore(ctx, store)
}
