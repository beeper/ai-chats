package connector

import "strings"

type AckReactionScope string

const (
	AckScopeAll          AckReactionScope = "all"
	AckScopeDirect       AckReactionScope = "direct"
	AckScopeGroupAll     AckReactionScope = "group-all"
	AckScopeGroupMention AckReactionScope = "group-mentions"
	AckScopeOff          AckReactionScope = "off"
	AckScopeNone         AckReactionScope = "none"
)

type AckReactionGateParams struct {
	Scope              AckReactionScope
	IsDirect           bool
	IsGroup            bool
	IsMentionableGroup bool
	RequireMention     bool
	CanDetectMention   bool
	EffectiveMention   bool
	ShouldBypass       bool
}

func normalizeAckScope(raw string) AckReactionScope {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	switch trimmed {
	case "all":
		return AckScopeAll
	case "direct":
		return AckScopeDirect
	case "group-all":
		return AckScopeGroupAll
	case "group-mentions":
		return AckScopeGroupMention
	case "off":
		return AckScopeOff
	case "none":
		return AckScopeNone
	default:
		return AckScopeGroupMention
	}
}

func shouldAckReaction(params AckReactionGateParams) bool {
	scope := params.Scope
	if scope == "" {
		scope = AckScopeGroupMention
	}
	switch scope {
	case AckScopeOff, AckScopeNone:
		return false
	case AckScopeAll:
		return true
	case AckScopeDirect:
		return params.IsDirect
	case AckScopeGroupAll:
		return params.IsGroup
	case AckScopeGroupMention:
		if !params.IsMentionableGroup {
			return false
		}
		if !params.RequireMention {
			return false
		}
		if !params.CanDetectMention {
			return false
		}
		return params.EffectiveMention || params.ShouldBypass
	default:
		return false
	}
}
