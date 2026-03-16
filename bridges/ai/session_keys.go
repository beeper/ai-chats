package ai

import (
	"strings"

	"github.com/beeper/agentremote/pkg/agents"
)

const (
	sessionScopePerSender = "per-sender"
	sessionScopeGlobal    = "global"
	defaultSessionMainKey = "main"
)

func normalizeSessionScope(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == sessionScopeGlobal {
		return sessionScopeGlobal
	}
	return sessionScopePerSender
}

func normalizeMainKey(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return defaultSessionMainKey
	}
	return trimmed
}

func buildAgentMainSessionKey(agentID string, mainKey string) string {
	normalized := normalizeAgentID(agentID)
	if normalized == "" {
		normalized = normalizeAgentID(agents.DefaultAgentID)
	}
	return "agent:" + normalized + ":" + normalizeMainKey(mainKey)
}

func resolveAgentMainSessionKey(cfg *Config, agentID string) string {
	mainKey := ""
	if cfg != nil && cfg.Session != nil {
		mainKey = cfg.Session.MainKey
	}
	return buildAgentMainSessionKey(agentID, mainKey)
}

func resolveAgentIdFromSessionKey(sessionKey string) string {
	parsed := parseAgentSessionKey(sessionKey)
	if parsed == "" {
		return normalizeAgentID(agents.DefaultAgentID)
	}
	return normalizeAgentID(parsed)
}

func toAgentStoreSessionKey(agentID string, requestKey string, mainKey string) string {
	raw := strings.TrimSpace(requestKey)
	if raw == "" || strings.EqualFold(raw, defaultSessionMainKey) {
		return buildAgentMainSessionKey(agentID, mainKey)
	}
	if strings.HasPrefix(raw, "!") {
		return raw
	}
	lowered := strings.ToLower(raw)
	if strings.HasPrefix(lowered, "agent:") {
		return lowered
	}
	return "agent:" + normalizeAgentID(agentID) + ":" + lowered
}

func canonicalizeMainSessionAlias(cfg *Config, agentID string, sessionKey string) string {
	raw := strings.TrimSpace(sessionKey)
	if raw == "" {
		return raw
	}
	mainKey := ""
	if cfg != nil && cfg.Session != nil {
		mainKey = cfg.Session.MainKey
	}
	normalizedAgent := normalizeAgentID(agentID)
	if normalizedAgent == "" {
		normalizedAgent = normalizeAgentID(agents.DefaultAgentID)
	}
	normalizedMain := normalizeMainKey(mainKey)
	agentMainKey := buildAgentMainSessionKey(normalizedAgent, normalizedMain)
	agentMainAlias := buildAgentMainSessionKey(normalizedAgent, defaultSessionMainKey)
	isMainAlias := raw == defaultSessionMainKey || raw == normalizedMain || raw == agentMainKey || raw == agentMainAlias
	if cfg != nil && cfg.Session != nil && normalizeSessionScope(cfg.Session.Scope) == sessionScopeGlobal && isMainAlias {
		return sessionScopeGlobal
	}
	if isMainAlias {
		return agentMainKey
	}
	return raw
}

func parseAgentSessionKey(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "agent:") {
		return ""
	}
	parts := strings.Split(trimmed, ":")
	if len(parts) < 3 {
		return ""
	}
	return parts[1]
}
