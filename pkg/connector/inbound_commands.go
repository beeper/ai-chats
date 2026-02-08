package connector

import (
	"regexp"
	"strings"
)

type inboundCommand struct {
	Name string
	Args string
	Raw  string
}

var commandColonRE = regexp.MustCompile(`^/([^\s:]+)\s*:(.*)$`)

func normalizeCommandBody(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/") {
		return trimmed
	}
	if idx := strings.IndexAny(trimmed, "\r\n"); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	if match := commandColonRE.FindStringSubmatch(trimmed); len(match) == 3 {
		command := match[1]
		rest := strings.TrimLeft(match[2], " \t")
		if rest != "" {
			return "/" + command + " " + rest
		}
		return "/" + command
	}
	return trimmed
}

func parseInboundCommand(raw string) (inboundCommand, bool) {
	normalized := normalizeCommandBody(raw)
	if !strings.HasPrefix(normalized, "/") {
		return inboundCommand{}, false
	}
	token := normalized
	args := ""
	if idx := strings.IndexAny(normalized, " \t"); idx >= 0 {
		token = normalized[:idx]
		args = strings.TrimSpace(normalized[idx+1:])
	}
	name := strings.ToLower(strings.TrimPrefix(token, "/"))
	if name == "" {
		return inboundCommand{}, false
	}
	return inboundCommand{Name: name, Args: args, Raw: normalized}, true
}

func splitCommandArgs(args string) (token string, rest string) {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return "", ""
	}
	if idx := strings.IndexAny(trimmed, " \t"); idx >= 0 {
		return trimmed[:idx], strings.TrimSpace(trimmed[idx+1:])
	}
	return trimmed, ""
}

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

var abortTriggers = map[string]struct{}{
	"stop":      {},
	"esc":       {},
	"abort":     {},
	"wait":      {},
	"exit":      {},
	"interrupt": {},
}

func isAbortTrigger(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return false
	}
	_, ok := abortTriggers[trimmed]
	return ok
}

var controlCommandNames = map[string]struct{}{
	"help":       {},
	"commands":   {},
	"status":     {},
	"context":    {},
	"tools":      {},
	"cron":       {},
	"model":      {},
	"think":      {},
	"verbose":    {},
	"reasoning":  {},
	"elevated":   {},
	"activation": {},
	"send":       {},
	"new":        {},
	"reset":      {},
	"queue":      {},
	"stop":       {},
	"abort":      {},
	"interrupt":  {},
	"exit":       {},
	"wait":       {},
	"esc":        {},
	"whoami":     {},
	"id":         {},
	"approve":    {},
}

func isControlCommandMessage(body string) bool {
	if cmd, ok := parseInboundCommand(body); ok {
		if _, exists := controlCommandNames[cmd.Name]; exists {
			return true
		}
	}
	return isAbortTrigger(body)
}
