package connector

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/textfs"
)

const defaultSessionStorePath = "sessions/sessions.json"

type sessionEntry struct {
	SessionID           string `json:"sessionId,omitempty"`
	UpdatedAt           int64  `json:"updatedAt,omitempty"`
	LastHeartbeatText   string `json:"lastHeartbeatText,omitempty"`
	LastHeartbeatSentAt int64  `json:"lastHeartbeatSentAt,omitempty"`
	LastChannel         string `json:"lastChannel,omitempty"`
	LastTo              string `json:"lastTo,omitempty"`
	LastAccountID       string `json:"lastAccountId,omitempty"`
	LastThreadID        string `json:"lastThreadId,omitempty"`
	QueueMode           string `json:"queueMode,omitempty"`
	QueueDebounceMs     *int   `json:"queueDebounceMs,omitempty"`
	QueueCap            *int   `json:"queueCap,omitempty"`
	QueueDrop           string `json:"queueDrop,omitempty"`
}

type sessionStore struct {
	Sessions map[string]sessionEntry `json:"sessions"`
}

type sessionStoreRef struct {
	AgentID string
	Path    string
}

var sessionStoreLocks sync.Map

func sessionStoreLockKey(ref sessionStoreRef) string {
	agent := strings.TrimSpace(ref.AgentID)
	path := strings.TrimSpace(ref.Path)
	if agent == "" {
		agent = "main"
	}
	if path == "" {
		path = defaultSessionStorePath
	}
	return agent + "|" + path
}

func sessionStoreLock(ref sessionStoreRef) *sync.Mutex {
	key := sessionStoreLockKey(ref)
	if val, ok := sessionStoreLocks.Load(key); ok {
		return val.(*sync.Mutex)
	}
	mu := &sync.Mutex{}
	actual, _ := sessionStoreLocks.LoadOrStore(key, mu)
	return actual.(*sync.Mutex)
}

func resolveSessionStorePath(cfg *Config, agentID string) string {
	raw := ""
	if cfg != nil && cfg.Session != nil {
		raw = cfg.Session.Store
	}
	normalizedAgent := normalizeAgentID(agentID)
	if normalizedAgent == "" {
		normalizedAgent = normalizeAgentID(agents.DefaultAgentID)
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultSessionStorePath
	}
	expanded := strings.ReplaceAll(trimmed, "{agentId}", normalizedAgent)
	if strings.HasPrefix(expanded, "~") {
		expanded = strings.TrimPrefix(expanded, "~")
		expanded = strings.TrimPrefix(expanded, "/")
	}
	if normalized, err := textfs.NormalizePath(expanded); err == nil {
		return normalized
	}
	return defaultSessionStorePath
}

func (oc *AIClient) sessionTextFSStore(agentID string) (*textfs.Store, error) {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.DB == nil {
		return nil, errors.New("session store not available")
	}
	bridgeID := string(oc.UserLogin.Bridge.DB.BridgeID)
	loginID := string(oc.UserLogin.ID)
	normalized := normalizeAgentID(agentID)
	if normalized == "" {
		normalized = normalizeAgentID(agents.DefaultAgentID)
	}
	return textfs.NewStore(oc.UserLogin.Bridge.DB.Database, bridgeID, loginID, normalized), nil
}

func (oc *AIClient) loadSessionStore(ctx context.Context, ref sessionStoreRef) (sessionStore, error) {
	store := sessionStore{Sessions: map[string]sessionEntry{}}
	textStore, err := oc.sessionTextFSStore(ref.AgentID)
	if err != nil {
		return store, err
	}
	entry, found, err := textStore.Read(ctx, ref.Path)
	if err != nil || !found {
		return store, nil
	}
	if err := json.Unmarshal([]byte(entry.Content), &store); err != nil {
		return sessionStore{Sessions: map[string]sessionEntry{}}, nil
	}
	if store.Sessions == nil {
		store.Sessions = map[string]sessionEntry{}
	}
	return store, nil
}

func (oc *AIClient) saveSessionStore(ctx context.Context, ref sessionStoreRef, store sessionStore) error {
	if store.Sessions == nil {
		store.Sessions = map[string]sessionEntry{}
	}
	payload, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	textStore, err := oc.sessionTextFSStore(ref.AgentID)
	if err != nil {
		return err
	}
	_, err = textStore.Write(ctx, ref.Path, string(payload))
	return err
}

func (oc *AIClient) getSessionEntry(ctx context.Context, ref sessionStoreRef, sessionKey string) (sessionEntry, bool) {
	if oc == nil || strings.TrimSpace(sessionKey) == "" {
		return sessionEntry{}, false
	}
	store, err := oc.loadSessionStore(ctx, ref)
	if err != nil {
		return sessionEntry{}, false
	}
	entry, ok := store.Sessions[sessionKey]
	return entry, ok
}

func (oc *AIClient) updateSessionEntry(ctx context.Context, ref sessionStoreRef, sessionKey string, updater func(entry sessionEntry) sessionEntry) {
	if oc == nil || strings.TrimSpace(sessionKey) == "" {
		return
	}
	lock := sessionStoreLock(ref)
	lock.Lock()
	defer lock.Unlock()
	store, err := oc.loadSessionStore(ctx, ref)
	if err != nil {
		return
	}
	entry := store.Sessions[sessionKey]
	entry = updater(entry)
	store.Sessions[sessionKey] = entry
	_ = oc.saveSessionStore(ctx, ref, store)
}

func mergeSessionEntry(existing sessionEntry, patch sessionEntry) sessionEntry {
	sessionID := patch.SessionID
	if sessionID == "" {
		sessionID = existing.SessionID
	}
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	updatedAt := time.Now().UnixMilli()
	if existing.UpdatedAt > updatedAt {
		updatedAt = existing.UpdatedAt
	}
	if patch.UpdatedAt > updatedAt {
		updatedAt = patch.UpdatedAt
	}
	next := existing
	if patch.LastHeartbeatText != "" {
		next.LastHeartbeatText = patch.LastHeartbeatText
	}
	if patch.LastHeartbeatSentAt != 0 {
		next.LastHeartbeatSentAt = patch.LastHeartbeatSentAt
	}
	if patch.LastChannel != "" {
		next.LastChannel = patch.LastChannel
	}
	if patch.LastTo != "" {
		next.LastTo = patch.LastTo
	}
	if patch.LastAccountID != "" {
		next.LastAccountID = patch.LastAccountID
	}
	if patch.LastThreadID != "" {
		next.LastThreadID = patch.LastThreadID
	}
	if patch.QueueMode != "" {
		next.QueueMode = patch.QueueMode
	}
	if patch.QueueDebounceMs != nil {
		next.QueueDebounceMs = patch.QueueDebounceMs
	}
	if patch.QueueCap != nil {
		next.QueueCap = patch.QueueCap
	}
	if patch.QueueDrop != "" {
		next.QueueDrop = patch.QueueDrop
	}
	next.SessionID = sessionID
	next.UpdatedAt = updatedAt
	return next
}

//lint:ignore U1000 Reserved for future session store routing.
func (oc *AIClient) resolveSessionStoreRef(agentID string) sessionStoreRef {
	cfg := (*Config)(nil)
	if oc != nil && oc.connector != nil {
		cfg = &oc.connector.Config
	}
	normalized := normalizeAgentID(agentID)
	if normalized == "" {
		normalized = normalizeAgentID(agents.DefaultAgentID)
	}
	path := resolveSessionStorePath(cfg, normalized)
	return sessionStoreRef{AgentID: normalized, Path: path}
}
