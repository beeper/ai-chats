package ai

import (
	"context"
	"strings"
	"sync"
	"time"
)

var sessionStoreLocks sync.Map

const (
	sessionScopePerSender = "per-sender"
	sessionScopeGlobal    = "global"
	defaultSessionMainKey = "main"
)

type heartbeatSessionResolution struct {
	StoreAgentID string
	SessionKey   string
	UpdatedAt    int64
}

func sessionStoreLockKey(ownerKey string, storeAgentID string, sessionKey string) string {
	agent := normalizeAgentID(storeAgentID)
	key := strings.TrimSpace(sessionKey)
	if key == "" {
		key = "main"
	}
	return ownerKey + "|" + agent + "|" + key
}

func sessionStoreLock(ownerKey string, storeAgentID string, sessionKey string) *sync.Mutex {
	key := sessionStoreLockKey(ownerKey, storeAgentID, sessionKey)
	if val, ok := sessionStoreLocks.Load(key); ok {
		return val.(*sync.Mutex)
	}
	mu := &sync.Mutex{}
	actual, _ := sessionStoreLocks.LoadOrStore(key, mu)
	return actual.(*sync.Mutex)
}

func (oc *AIClient) normalizedSessionAgentID(agentID string) string {
	resolvedAgent := normalizeAgentID(agentID)
	if resolvedAgent == "" {
		return "default"
	}
	return resolvedAgent
}

func (oc *AIClient) sessionScope() string {
	cfg := (*Config)(nil)
	if oc != nil && oc.connector != nil {
		cfg = &oc.connector.Config
	}
	scope := sessionScopePerSender
	if cfg != nil && cfg.Session != nil {
		if trimmed := strings.ToLower(strings.TrimSpace(cfg.Session.Scope)); trimmed == sessionScopeGlobal {
			scope = sessionScopeGlobal
		}
	}
	return scope
}

func (oc *AIClient) sessionMainKey(agentID string) string {
	resolvedAgent := oc.normalizedSessionAgentID(agentID)
	if oc.sessionScope() == sessionScopeGlobal {
		return sessionScopeGlobal
	}
	normalizedMainKey := defaultSessionMainKey
	cfg := (*Config)(nil)
	if oc != nil && oc.connector != nil {
		cfg = &oc.connector.Config
	}
	if cfg != nil && cfg.Session != nil {
		if trimmed := strings.ToLower(strings.TrimSpace(cfg.Session.MainKey)); trimmed != "" {
			normalizedMainKey = trimmed
		}
	}
	return "agent:" + resolvedAgent + ":" + normalizedMainKey
}

func (oc *AIClient) sessionStoreAgentID(agentID string) string {
	if oc.sessionScope() == sessionScopeGlobal {
		return sessionScopeGlobal
	}
	return oc.normalizedSessionAgentID(agentID)
}

func (oc *AIClient) lastRoutedSessionKey(ctx context.Context, agentID string) (string, bool) {
	return "", false
}

func (oc *AIClient) storedSessionUpdatedAt(ctx context.Context, storeAgentID string, sessionKey string) (int64, bool) {
	if oc == nil || strings.TrimSpace(sessionKey) == "" {
		return 0, false
	}
	return 0, false
}

func (oc *AIClient) saveStoredSessionUpdatedAt(ctx context.Context, storeAgentID string, sessionKey string, updatedAt int64) error {
	return nil
}

func (oc *AIClient) touchStoredSession(ctx context.Context, storeAgentID string, sessionKey string, minUpdatedAt int64) {
	if oc == nil || strings.TrimSpace(sessionKey) == "" {
		return
	}
	lock := sessionStoreLock("ai", storeAgentID, sessionKey)
	lock.Lock()
	defer lock.Unlock()
	_ = time.Now()
}
