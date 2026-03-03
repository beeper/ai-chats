package connector

import (
	"context"
	"strings"

	"github.com/beeper/ai-bridge/pkg/agents"
)

type heartbeatSessionResolution struct {
	StoreRef   sessionStoreRef
	SessionKey string
	Entry      *sessionEntry
}

// sessionResolutionContext holds intermediate values computed when resolving
// the main session store reference and key. This avoids recomputing them in
// resolveHeartbeatSession which needs the same values.
type sessionResolutionContext struct {
	Cfg            *Config
	ResolvedAgent  string
	Scope          string
	StoreRef       sessionStoreRef
	MainSessionKey string
}

// resolveMainSessionContext computes the session store reference, main session
// key, and related intermediates for the given agent, factoring in scope
// configuration. This is the common logic shared by resolveHeartbeatSession
// and resolveHeartbeatMainSessionRef.
func (oc *AIClient) resolveMainSessionContext(agentID string) sessionResolutionContext {
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
	mainSessionKey := resolveAgentMainSessionKey(cfg, resolvedAgent)
	if scope == sessionScopeGlobal {
		mainSessionKey = sessionScopeGlobal
	}
	storeAgentID := resolvedAgent
	if scope == sessionScopeGlobal {
		storeAgentID = normalizeAgentID(agents.DefaultAgentID)
		if storeAgentID == "" {
			storeAgentID = resolvedAgent
		}
	}
	storeRef := sessionStoreRef{
		AgentID: storeAgentID,
		Path:    resolveSessionStorePath(cfg, storeAgentID),
	}
	return sessionResolutionContext{
		Cfg:            cfg,
		ResolvedAgent:  resolvedAgent,
		Scope:          scope,
		StoreRef:       storeRef,
		MainSessionKey: mainSessionKey,
	}
}

func (oc *AIClient) resolveHeartbeatSession(agentID string, heartbeat *HeartbeatConfig) heartbeatSessionResolution {
	rc := oc.resolveMainSessionContext(agentID)

	store, _ := oc.loadSessionStore(context.Background(), rc.StoreRef)
	mainEntry, hasMain := store.Sessions[rc.MainSessionKey]

	makeResult := func(key string, entry *sessionEntry) heartbeatSessionResolution {
		return heartbeatSessionResolution{StoreRef: rc.StoreRef, SessionKey: key, Entry: entry}
	}
	mainResult := func() heartbeatSessionResolution {
		if hasMain {
			e := mainEntry
			return makeResult(rc.MainSessionKey, &e)
		}
		return makeResult(rc.MainSessionKey, nil)
	}

	if rc.Scope == sessionScopeGlobal {
		return mainResult()
	}

	trimmed := ""
	if heartbeat != nil && heartbeat.Session != nil {
		trimmed = strings.TrimSpace(*heartbeat.Session)
	}
	if trimmed == "" || strings.EqualFold(trimmed, "main") || strings.EqualFold(trimmed, "global") {
		return mainResult()
	}

	if strings.HasPrefix(trimmed, "!") {
		if entry, ok := store.Sessions[trimmed]; ok {
			e := entry
			return makeResult(trimmed, &e)
		}
		return makeResult(trimmed, nil)
	}

	candidate := toAgentStoreSessionKey(rc.ResolvedAgent, trimmed, "")
	if rc.Cfg != nil && rc.Cfg.Session != nil {
		candidate = toAgentStoreSessionKey(rc.ResolvedAgent, trimmed, rc.Cfg.Session.MainKey)
	}
	canonical := canonicalizeMainSessionAlias(rc.Cfg, rc.ResolvedAgent, candidate)
	if canonical != sessionScopeGlobal {
		sessionAgent := resolveAgentIdFromSessionKey(canonical)
		if sessionAgent == rc.ResolvedAgent {
			if entry, ok := store.Sessions[canonical]; ok {
				e := entry
				return makeResult(canonical, &e)
			}
			return makeResult(canonical, nil)
		}
	}

	return mainResult()
}

func (oc *AIClient) resolveHeartbeatMainSessionRef(agentID string) (sessionStoreRef, string) {
	rc := oc.resolveMainSessionContext(agentID)
	return rc.StoreRef, rc.MainSessionKey
}
