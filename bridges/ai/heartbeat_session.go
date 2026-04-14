package ai

import (
	"context"
	"strings"

	"github.com/beeper/agentremote/pkg/agents"
)

const (
	sessionScopePerSender = "per-sender"
	sessionScopeGlobal    = "global"
	defaultSessionMainKey = "main"
)

type sessionRouting struct {
	AgentID      string
	StoreAgentID string
	MainKey      string
	Scope        string
}

type heartbeatSessionResolution struct {
	StoreAgentID string
	SessionKey   string
	UpdatedAt    int64
}

func (oc *AIClient) resolveSessionRouting(agentID string) sessionRouting {
	cfg := (*Config)(nil)
	if oc != nil && oc.connector != nil {
		cfg = &oc.connector.Config
	}
	resolvedAgent := normalizeAgentID(agentID)
	if resolvedAgent == "" {
		resolvedAgent = normalizeAgentID(agents.DefaultAgentID)
	}
	scope := sessionScopePerSender
	if cfg != nil && cfg.Session != nil {
		if trimmed := strings.ToLower(strings.TrimSpace(cfg.Session.Scope)); trimmed == sessionScopeGlobal {
			scope = sessionScopeGlobal
		}
	}
	normalizedMainKey := defaultSessionMainKey
	if cfg != nil && cfg.Session != nil {
		if trimmed := strings.ToLower(strings.TrimSpace(cfg.Session.MainKey)); trimmed != "" {
			normalizedMainKey = trimmed
		}
	}
	mainSessionKey := "agent:" + resolvedAgent + ":" + normalizedMainKey
	storeAgentID := resolvedAgent
	if scope == sessionScopeGlobal {
		mainSessionKey = sessionScopeGlobal
		storeAgentID = sessionScopeGlobal
	}
	return sessionRouting{
		AgentID:      resolvedAgent,
		StoreAgentID: storeAgentID,
		MainKey:      mainSessionKey,
		Scope:        scope,
	}
}

func (oc *AIClient) resolveHeartbeatSession(agentID string, heartbeat *HeartbeatConfig) heartbeatSessionResolution {
	routing := oc.resolveSessionRouting(agentID)
	lookup := func(key string) (int64, bool) {
		return oc.loadSessionUpdatedAt(context.Background(), routing.StoreAgentID, key)
	}
	if routing.Scope == sessionScopeGlobal {
		return heartbeatSessionResolution{StoreAgentID: routing.StoreAgentID, SessionKey: routing.MainKey}
	}

	trimmed := ""
	if heartbeat != nil && heartbeat.Session != nil {
		trimmed = strings.TrimSpace(*heartbeat.Session)
	}
	isMainAlias := func(raw string) bool {
		candidate := strings.TrimSpace(raw)
		if candidate == "" {
			return false
		}
		normalizedMain := strings.ToLower(strings.TrimSpace(routing.MainKey))
		if normalizedMain == "" {
			normalizedMain = defaultSessionMainKey
		}
		agentMainAlias := "agent:" + routing.AgentID + ":" + defaultSessionMainKey
		return strings.EqualFold(candidate, defaultSessionMainKey) ||
			strings.EqualFold(candidate, sessionScopeGlobal) ||
			strings.EqualFold(candidate, normalizedMain) ||
			strings.EqualFold(candidate, routing.MainKey) ||
			strings.EqualFold(candidate, agentMainAlias)
	}
	sessionKey := routing.MainKey
	if routing.Scope != sessionScopeGlobal && !isMainAlias(trimmed) {
		if strings.HasPrefix(trimmed, "!") {
			sessionKey = trimmed
		} else {
			candidate := strings.ToLower(trimmed)
			if candidate == "" || strings.EqualFold(candidate, defaultSessionMainKey) {
				candidate = routing.MainKey
			} else if !strings.HasPrefix(candidate, "agent:") {
				candidate = "agent:" + routing.AgentID + ":" + candidate
			}
			if strings.HasPrefix(candidate, "agent:"+routing.AgentID+":") && !isMainAlias(candidate) {
				sessionKey = candidate
			}
		}
	}
	if sessionKey == routing.MainKey {
		return heartbeatSessionResolution{StoreAgentID: routing.StoreAgentID, SessionKey: sessionKey}
	}
	if updatedAt, ok := lookup(sessionKey); ok {
		return heartbeatSessionResolution{StoreAgentID: routing.StoreAgentID, SessionKey: sessionKey, UpdatedAt: updatedAt}
	}
	return heartbeatSessionResolution{StoreAgentID: routing.StoreAgentID, SessionKey: sessionKey}
}
