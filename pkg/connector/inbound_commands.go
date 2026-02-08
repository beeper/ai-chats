package connector

import (
	"strings"
)

func normalizeThinkLevel(raw string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	switch key {
	case "off", "false", "no", "0":
		return "off", true
	case "on", "enable", "enabled":
		return "low", true
	case "min", "minimal", "think":
		return "minimal", true
	case "low", "thinkhard", "think-hard", "think_hard":
		return "low", true
	case "mid", "med", "medium", "thinkharder", "think-harder", "harder":
		return "medium", true
	case "high", "ultra", "ultrathink", "thinkhardest", "highest", "max":
		return "high", true
	case "xhigh", "x-high", "x_high":
		return "xhigh", true
	}
	return "", false
}

func normalizeVerboseLevel(raw string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	switch key {
	case "off", "false", "no", "0":
		return "off", true
	case "full", "all", "everything":
		return "full", true
	case "on", "minimal", "true", "yes", "1":
		return "on", true
	}
	return "", false
}

func normalizeReasoningLevel(raw string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	switch key {
	case "off", "false", "no", "0":
		return "off", true
	case "on", "true", "yes", "1", "stream":
		return "on", true
	case "low", "medium", "high", "xhigh":
		return key, true
	}
	return "", false
}

func normalizeElevatedLevel(raw string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	switch key {
	case "off", "false", "no", "0":
		return "off", true
	case "full", "auto", "auto-approve", "autoapprove":
		return "full", true
	case "ask", "prompt", "approval", "approve":
		return "ask", true
	case "on", "true", "yes", "1":
		return "on", true
	}
	return "", false
}

func normalizeSendPolicy(raw string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	switch key {
	case "allow", "on":
		return "allow", true
	case "deny", "off":
		return "deny", true
	case "inherit", "default", "reset":
		return "inherit", true
	}
	return "", false
}

func normalizeGroupActivation(raw string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	switch key {
	case "mention":
		return "mention", true
	case "always":
		return "always", true
	}
	return "", false
}
