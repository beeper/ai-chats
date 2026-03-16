package ai

import "strings"

func normalizeOwnerAllowEntry(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "matrix:") {
		trimmed = strings.TrimSpace(trimmed[len("matrix:"):])
	}
	return trimmed
}

func normalizeOwnerAllowlist(entries []string) (bool, map[string]struct{}) {
	allowAny := false
	allowed := make(map[string]struct{})
	for _, entry := range entries {
		normalized := normalizeOwnerAllowEntry(entry)
		if normalized == "" {
			continue
		}
		if normalized == "*" {
			allowAny = true
			continue
		}
		allowed[normalized] = struct{}{}
	}
	return allowAny, allowed
}

func isOwnerAllowlistConfigured(cfg *Config) bool {
	if cfg == nil || cfg.Commands == nil {
		return false
	}
	for _, entry := range cfg.Commands.OwnerAllowFrom {
		if strings.TrimSpace(entry) != "" {
			return true
		}
	}
	return false
}

func isOwnerAllowed(cfg *Config, senderID string) bool {
	if !isOwnerAllowlistConfigured(cfg) {
		return true
	}
	allowAny, allowed := normalizeOwnerAllowlist(cfg.Commands.OwnerAllowFrom)
	if allowAny {
		return true
	}
	normalized := normalizeOwnerAllowEntry(senderID)
	if normalized == "" {
		return false
	}
	_, ok := allowed[normalized]
	return ok
}
