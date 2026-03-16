package ai

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
)

type mediaUnderstandingScopeDecision string

const (
	scopeAllow mediaUnderstandingScopeDecision = "allow"
	scopeDeny  mediaUnderstandingScopeDecision = "deny"
)

func normalizeScopeDecision(value string) (mediaUnderstandingScopeDecision, bool) {
	normalized := strings.TrimSpace(strings.ToLower(value))
	switch normalized {
	case "allow":
		return scopeAllow, true
	case "deny":
		return scopeDeny, true
	default:
		return "", false
	}
}

func normalizeScopeMatch(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}

func normalizeMediaUnderstandingChatType(raw string) string {
	normalized := strings.TrimSpace(strings.ToLower(raw))
	switch normalized {
	case "group", "room":
		return "group"
	case "direct", "dm", "private":
		return "direct"
	default:
		return normalized
	}
}

func resolveMediaUnderstandingScopeDecision(scope *MediaUnderstandingScopeConfig, channel, chatType, sessionKey string) mediaUnderstandingScopeDecision {
	if scope == nil {
		return scopeAllow
	}

	channel = normalizeScopeMatch(channel)
	chatType = normalizeMediaUnderstandingChatType(chatType)
	sessionKey = normalizeScopeMatch(sessionKey)

	for _, rule := range scope.Rules {
		action, ok := normalizeScopeDecision(rule.Action)
		if !ok {
			action = scopeAllow
		}
		match := rule.Match
		if match != nil {
			matchChannel := normalizeScopeMatch(match.Channel)
			matchChatType := normalizeMediaUnderstandingChatType(match.ChatType)
			matchPrefix := normalizeScopeMatch(match.KeyPrefix)

			if matchChannel != "" && matchChannel != channel {
				continue
			}
			if matchChatType != "" && matchChatType != chatType {
				continue
			}
			if matchPrefix != "" && !strings.HasPrefix(sessionKey, matchPrefix) {
				continue
			}
		}
		return action
	}

	if decision, ok := normalizeScopeDecision(scope.Default); ok {
		return decision
	}
	return scopeAllow
}

func (oc *AIClient) mediaUnderstandingScopeDecision(ctx context.Context, portal *bridgev2.Portal, scope *MediaUnderstandingScopeConfig) mediaUnderstandingScopeDecision {
	if scope == nil {
		return scopeAllow
	}
	channel := "matrix"
	chatType := "direct"
	sessionKey := ""
	if portal != nil {
		if portal.MXID != "" {
			sessionKey = portal.MXID.String()
		} else if portal.PortalKey.String() != "" {
			sessionKey = portal.PortalKey.String()
		}
	}
	if oc != nil && portal != nil && oc.isGroupChat(ctx, portal) {
		chatType = "group"
	}
	return resolveMediaUnderstandingScopeDecision(scope, channel, chatType, sessionKey)
}
