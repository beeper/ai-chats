package ai

import (
	"context"
	"strings"

	"github.com/beeper/agentremote/pkg/agents"
)

type sessionRouting struct {
	AgentID  string
	StoreRef sessionStoreRef
	MainKey  string
	Scope    string
}

type heartbeatSessionResolution struct {
	StoreRef   sessionStoreRef
	SessionKey string
	UpdatedAt  int64
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
		AgentID:  resolvedAgent,
		StoreRef: loginScopeForClient(oc).sessionStoreRef(storeAgentID),
		MainKey:  mainSessionKey,
		Scope:    scope,
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
	lookup := func(key string) (sessionEntry, bool) {
		return oc.getSessionEntry(context.Background(), routing.StoreRef, key)
	}
	if routing.Scope == sessionScopeGlobal {
		return heartbeatSessionResolution{StoreRef: routing.StoreRef, SessionKey: routing.MainKey}
	}

	trimmed := ""
	if heartbeat != nil && heartbeat.Session != nil {
		trimmed = strings.TrimSpace(*heartbeat.Session)
	}
	sessionKey := routing.resolveRequestedSession(trimmed)
	if sessionKey == routing.MainKey {
		return heartbeatSessionResolution{StoreRef: routing.StoreRef, SessionKey: sessionKey}
	}
	if entry, ok := lookup(sessionKey); ok {
		return heartbeatSessionResolution{StoreRef: routing.StoreRef, SessionKey: sessionKey, UpdatedAt: entry.UpdatedAt}
	}
	return heartbeatSessionResolution{StoreRef: routing.StoreRef, SessionKey: sessionKey}
}
