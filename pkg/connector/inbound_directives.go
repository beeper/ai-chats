package connector

import (
	"regexp"
	"strings"
)

var (
	thinkDirectiveRE     = regexp.MustCompile(`(?i)(?:^|\s)/(?:thinking|think|t)(?:$|\s|:)`)
	verboseDirectiveRE   = regexp.MustCompile(`(?i)(?:^|\s)/(?:verbose|v)(?:$|\s|:)`)
	reasoningDirectiveRE = regexp.MustCompile(`(?i)(?:^|\s)/(?:reasoning|reason)(?:$|\s|:)`)
	elevatedDirectiveRE  = regexp.MustCompile(`(?i)(?:^|\s)/(?:elevated|elev)(?:$|\s|:)`)
	statusDirectiveRE    = regexp.MustCompile(`(?i)(?:^|\s)/(?:status)(?:$|\s|:)(?:\s*:\s*)?`)
	modelDirectiveRE     = regexp.MustCompile(`(?i)(?:^|\s)/(?:model)(?:$|\s|:)`)
	helpDirectiveRE      = regexp.MustCompile(`(?i)(?:^|\s)/(?:help)(?:$|\s|:)(?:\s*:\s*)?`)
	commandsDirectiveRE  = regexp.MustCompile(`(?i)(?:^|\s)/(?:commands)(?:$|\s|:)(?:\s*:\s*)?`)
	whoamiDirectiveRE    = regexp.MustCompile(`(?i)(?:^|\s)/(?:whoami|id)(?:$|\s|:)(?:\s*:\s*)?`)
)

type inlineDirectives struct {
	cleaned string

	hasThink     bool
	thinkLevel   string
	rawThink     string
	invalidThink bool

	hasVerbose     bool
	verboseLevel   string
	rawVerbose     string
	invalidVerbose bool

	hasReasoning     bool
	reasoningLevel   string
	rawReasoning     string
	invalidReasoning bool

	hasElevated     bool
	elevatedLevel   string
	rawElevated     string
	invalidElevated bool

	hasStatus bool

	hasModel bool
	rawModel string

	queue QueueDirective
}

func (d inlineDirectives) hasAnyDirective() bool {
	return d.hasThink || d.hasVerbose || d.hasReasoning || d.hasElevated || d.hasStatus || d.hasModel || d.queue.HasDirective
}

type levelDirective struct {
	cleaned string
	level   string
	raw     string
	has     bool
}

func parseInlineDirectives(body string) inlineDirectives {
	out := inlineDirectives{cleaned: strings.TrimSpace(body)}
	if out.cleaned == "" {
		out.queue = QueueDirective{Cleaned: out.cleaned}
		return out
	}

	think := extractLevelDirectiveRE(out.cleaned, thinkDirectiveRE, normalizeThinkLevel)
	out.cleaned = think.cleaned
	out.hasThink = think.has
	out.thinkLevel = think.level
	out.rawThink = think.raw
	out.invalidThink = think.has && think.level == "" && think.raw != ""

	verbose := extractLevelDirectiveRE(out.cleaned, verboseDirectiveRE, normalizeVerboseLevel)
	out.cleaned = verbose.cleaned
	out.hasVerbose = verbose.has
	out.verboseLevel = verbose.level
	out.rawVerbose = verbose.raw
	out.invalidVerbose = verbose.has && verbose.level == "" && verbose.raw != ""

	reasoning := extractLevelDirectiveRE(out.cleaned, reasoningDirectiveRE, normalizeReasoningLevel)
	out.cleaned = reasoning.cleaned
	out.hasReasoning = reasoning.has
	out.reasoningLevel = reasoning.level
	out.rawReasoning = reasoning.raw
	out.invalidReasoning = reasoning.has && reasoning.level == "" && reasoning.raw != ""

	elevated := extractLevelDirectiveRE(out.cleaned, elevatedDirectiveRE, normalizeElevatedLevel)
	out.cleaned = elevated.cleaned
	out.hasElevated = elevated.has
	out.elevatedLevel = elevated.level
	out.rawElevated = elevated.raw
	out.invalidElevated = elevated.has && elevated.level == "" && elevated.raw != ""

	status := extractSimpleDirectiveRE(out.cleaned, statusDirectiveRE)
	out.cleaned = status.cleaned
	out.hasStatus = status.hasDirective

	model := extractModelDirective(out.cleaned)
	out.cleaned = model.cleaned
	out.hasModel = model.hasDirective
	out.rawModel = model.rawModel

	queue := extractQueueDirective(out.cleaned)
	out.cleaned = queue.Cleaned
	out.queue = queue

	return out
}

func extractLevelDirective(
	body string,
	names []string,
	normalize func(string) (string, bool),
) levelDirective {
	return extractLevelDirectiveRE(body, directiveREForNames(names), normalize)
}

func extractLevelDirectiveRE(
	body string,
	re *regexp.Regexp,
	normalize func(string) (string, bool),
) levelDirective {
	match := matchLevelDirectiveRE(body, re)
	if match == nil {
		return levelDirective{cleaned: strings.TrimSpace(body)}
	}
	raw := match.raw
	level := ""
	if raw != "" {
		if normalized, ok := normalize(raw); ok {
			level = normalized
		}
	}
	cleaned := strings.TrimSpace(collapseWhitespace(body[:match.start] + " " + body[match.end:]))
	return levelDirective{
		cleaned: cleaned,
		level:   level,
		raw:     raw,
		has:     true,
	}
}

type directiveMatch struct {
	start int
	end   int
	raw   string
}

func matchLevelDirective(body string, names []string) *directiveMatch {
	return matchLevelDirectiveRE(body, directiveREForNames(names))
}

func matchLevelDirectiveRE(body string, re *regexp.Regexp) *directiveMatch {
	if body == "" {
		return nil
	}
	loc := re.FindStringIndex(body)
	if loc == nil {
		return nil
	}
	start := loc[0]
	end := loc[1]
	i := end
	for i < len(body) && isWhitespace(body[i]) {
		i++
	}
	if i < len(body) && body[i] == ':' {
		i++
		for i < len(body) && isWhitespace(body[i]) {
			i++
		}
	}
	argStart := i
	for i < len(body) && isDirectiveLevelChar(body[i]) {
		i++
	}
	raw := ""
	if i > argStart {
		raw = body[argStart:i]
	}
	end = i
	return &directiveMatch{start: start, end: end, raw: raw}
}

func extractSimpleDirective(body string, names []string) (out struct {
	cleaned      string
	hasDirective bool
}) {
	return extractSimpleDirectiveRE(body, simpleDirectiveREForNames(names))
}

func extractSimpleDirectiveRE(body string, re *regexp.Regexp) (out struct {
	cleaned      string
	hasDirective bool
}) {
	if body == "" {
		out.cleaned = ""
		return out
	}
	loc := re.FindStringIndex(body)
	if loc == nil {
		out.cleaned = strings.TrimSpace(body)
		return out
	}
	cleaned := body[:loc[0]] + " " + body[loc[1]:]
	out.cleaned = strings.TrimSpace(collapseWhitespace(cleaned))
	out.hasDirective = true
	return out
}

type modelDirective struct {
	cleaned      string
	rawModel     string
	hasDirective bool
}

func extractModelDirective(body string) modelDirective {
	if body == "" {
		return modelDirective{cleaned: ""}
	}
	match := matchModelDirective(body)
	if match == nil {
		return modelDirective{cleaned: strings.TrimSpace(body)}
	}
	cleaned := strings.TrimSpace(collapseWhitespace(body[:match.start] + " " + body[match.end:]))
	return modelDirective{
		cleaned:      cleaned,
		rawModel:     match.raw,
		hasDirective: true,
	}
}

func matchModelDirective(body string) *directiveMatch {
	loc := modelDirectiveRE.FindStringIndex(body)
	if loc == nil {
		return nil
	}
	start := loc[0]
	end := loc[1]
	i := end
	for i < len(body) && isWhitespace(body[i]) {
		i++
	}
	if i < len(body) && body[i] == ':' {
		i++
		for i < len(body) && isWhitespace(body[i]) {
			i++
		}
	}
	argStart := i
	for i < len(body) && isModelTokenChar(body[i]) {
		i++
	}
	raw := ""
	if i > argStart {
		raw = body[argStart:i]
	}
	end = i
	return &directiveMatch{start: start, end: end, raw: raw}
}

func extractInlineShortcut(body string, names []string) (string, bool) {
	out := extractSimpleDirective(body, names)
	return out.cleaned, out.hasDirective
}

var levelDirectiveREs = map[string]*regexp.Regexp{
	"thinking,think,t": thinkDirectiveRE,
	"verbose,v":        verboseDirectiveRE,
	"reasoning,reason": reasoningDirectiveRE,
	"elevated,elev":    elevatedDirectiveRE,
}

var simpleDirectiveREs = map[string]*regexp.Regexp{
	"status":    statusDirectiveRE,
	"help":      helpDirectiveRE,
	"commands":  commandsDirectiveRE,
	"whoami,id": whoamiDirectiveRE,
}

func directiveREForNames(names []string) *regexp.Regexp {
	key := strings.Join(names, ",")
	if re, ok := levelDirectiveREs[key]; ok {
		return re
	}
	namePattern := strings.Join(escapeDirectiveNames(names), "|")
	return regexp.MustCompile(`(?i)(?:^|\s)/(?:` + namePattern + `)(?:$|\s|:)`)
}

func simpleDirectiveREForNames(names []string) *regexp.Regexp {
	key := strings.Join(names, ",")
	if re, ok := simpleDirectiveREs[key]; ok {
		return re
	}
	namePattern := strings.Join(escapeDirectiveNames(names), "|")
	return regexp.MustCompile(`(?i)(?:^|\s)/(?:` + namePattern + `)(?:$|\s|:)(?:\s*:\s*)?`)
}

func escapeDirectiveNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			out = append(out, regexp.QuoteMeta(trimmed))
		}
	}
	return out
}

func collapseWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func isWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func isDirectiveLevelChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '-'
}

func isModelTokenChar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z':
		return true
	case b >= 'A' && b <= 'Z':
		return true
	case b >= '0' && b <= '9':
		return true
	case b == '-' || b == '_' || b == '.' || b == '/' || b == ':' || b == '@':
		return true
	default:
		return false
	}
}
