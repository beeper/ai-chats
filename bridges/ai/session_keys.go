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

func isMainSessionAlias(agentID string, mainKey string, raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	normalizedMain := normalizeMainKey(mainKey)
	agentMainKey := buildAgentMainSessionKey(agentID, normalizedMain)
	agentMainAlias := buildAgentMainSessionKey(agentID, defaultSessionMainKey)
	return strings.EqualFold(trimmed, defaultSessionMainKey) ||
		strings.EqualFold(trimmed, sessionScopeGlobal) ||
		strings.EqualFold(trimmed, normalizedMain) ||
		strings.EqualFold(trimmed, agentMainKey) ||
		strings.EqualFold(trimmed, agentMainAlias)
}

func toAgentStoreSessionKey(agentID string, requestKey string) string {
	raw := strings.TrimSpace(requestKey)
	if raw == "" || strings.EqualFold(raw, defaultSessionMainKey) {
		return buildAgentMainSessionKey(agentID, "")
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
