package ai

import (
	"context"
	"strings"

	"github.com/beeper/agentremote/pkg/agents"
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
		scope = normalizeSessionScope(cfg.Session.Scope)
	}
	mainSessionKey := buildAgentMainSessionKey(resolvedAgent, "")
	if cfg != nil && cfg.Session != nil {
		mainSessionKey = buildAgentMainSessionKey(resolvedAgent, cfg.Session.MainKey)
	}
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

func (routing sessionRouting) resolveRequestedSession(session string) string {
	trimmed := strings.TrimSpace(session)
	if routing.Scope == sessionScopeGlobal || isMainSessionAlias(routing.AgentID, routing.MainKey, trimmed) {
		return routing.MainKey
	}
	if strings.HasPrefix(trimmed, "!") {
		return trimmed
	}
	candidate := toAgentStoreSessionKey(routing.AgentID, trimmed)
	if !strings.HasPrefix(candidate, "agent:"+routing.AgentID+":") || isMainSessionAlias(routing.AgentID, routing.MainKey, candidate) {
		return routing.MainKey
	}
	return candidate
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
	sessionKey := routing.resolveRequestedSession(trimmed)
	if sessionKey == routing.MainKey {
		return heartbeatSessionResolution{StoreAgentID: routing.StoreAgentID, SessionKey: sessionKey}
	}
	if updatedAt, ok := lookup(sessionKey); ok {
		return heartbeatSessionResolution{StoreAgentID: routing.StoreAgentID, SessionKey: sessionKey, UpdatedAt: updatedAt}
	}
	return heartbeatSessionResolution{StoreAgentID: routing.StoreAgentID, SessionKey: sessionKey}
}
