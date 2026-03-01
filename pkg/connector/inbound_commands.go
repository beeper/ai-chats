package connector

import (
	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

var thinkLevelAliases = map[string]string{
	"off": "off", "false": "off", "no": "off", "0": "off",
	"on": "low", "enable": "low", "enabled": "low",
	"min": "minimal", "minimal": "minimal", "think": "minimal",
	"low": "low", "thinkhard": "low", "think-hard": "low", "think_hard": "low",
	"mid": "medium", "med": "medium", "medium": "medium", "thinkharder": "medium", "think-harder": "medium", "harder": "medium",
	"high": "high", "ultra": "high", "ultrathink": "high", "thinkhardest": "high", "highest": "high", "max": "high",
	"xhigh": "xhigh", "x-high": "xhigh", "x_high": "xhigh",
}

var verboseLevelAliases = map[string]string{
	"off": "off", "false": "off", "no": "off", "0": "off",
	"full": "full", "all": "full", "everything": "full",
	"on": "on", "minimal": "on", "true": "on", "yes": "on", "1": "on",
}

var reasoningLevelAliases = map[string]string{
	"off": "off", "false": "off", "no": "off", "0": "off",
	"on": "on", "true": "on", "yes": "on", "1": "on", "stream": "on",
	"low": "low", "medium": "medium", "high": "high", "xhigh": "xhigh",
}

var sendPolicyAliases = map[string]string{
	"allow": "allow", "on": "allow",
	"deny": "deny", "off": "deny",
	"inherit": "inherit", "default": "inherit", "reset": "inherit",
}

var groupActivationAliases = map[string]string{
	"mention": "mention",
	"always":  "always",
}

func normalizeThinkLevel(raw string) (string, bool) {
	return stringutil.NormalizeEnum(raw, thinkLevelAliases)
}

func normalizeVerboseLevel(raw string) (string, bool) {
	return stringutil.NormalizeEnum(raw, verboseLevelAliases)
}

func normalizeReasoningLevel(raw string) (string, bool) {
	return stringutil.NormalizeEnum(raw, reasoningLevelAliases)
}

func normalizeSendPolicy(raw string) (string, bool) {
	return stringutil.NormalizeEnum(raw, sendPolicyAliases)
}

func normalizeGroupActivation(raw string) (string, bool) {
	return stringutil.NormalizeEnum(raw, groupActivationAliases)
}
