package connector

import (
	"regexp"
	"strings"
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

	think := extractLevelDirective(out.cleaned, []string{"thinking", "think", "t"}, normalizeThinkLevel)
	out.cleaned = think.cleaned
	out.hasThink = think.has
	out.thinkLevel = think.level
	out.rawThink = think.raw
	out.invalidThink = think.has && think.level == "" && think.raw != ""

	verbose := extractLevelDirective(out.cleaned, []string{"verbose", "v"}, normalizeVerboseLevel)
	out.cleaned = verbose.cleaned
	out.hasVerbose = verbose.has
	out.verboseLevel = verbose.level
	out.rawVerbose = verbose.raw
	out.invalidVerbose = verbose.has && verbose.level == "" && verbose.raw != ""

	reasoning := extractLevelDirective(out.cleaned, []string{"reasoning", "reason"}, normalizeReasoningLevel)
	out.cleaned = reasoning.cleaned
	out.hasReasoning = reasoning.has
	out.reasoningLevel = reasoning.level
	out.rawReasoning = reasoning.raw
	out.invalidReasoning = reasoning.has && reasoning.level == "" && reasoning.raw != ""

	elevated := extractLevelDirective(out.cleaned, []string{"elevated", "elev"}, normalizeElevatedLevel)
	out.cleaned = elevated.cleaned
	out.hasElevated = elevated.has
	out.elevatedLevel = elevated.level
	out.rawElevated = elevated.raw
	out.invalidElevated = elevated.has && elevated.level == "" && elevated.raw != ""

	status := extractSimpleDirective(out.cleaned, []string{"status"})
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
	match := matchLevelDirective(body, names)
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
	if body == "" {
		return nil
	}
	namePattern := strings.Join(escapeDirectiveNames(names), "|")
	re := regexp.MustCompile(`(?i)(?:^|\s)/(?:` + namePattern + `)(?:$|\s|:)`)
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
	if body == "" {
		out.cleaned = ""
		return out
	}
	namePattern := strings.Join(escapeDirectiveNames(names), "|")
	re := regexp.MustCompile(`(?i)(?:^|\s)/(?:` + namePattern + `)(?:$|\s|:)(?:\s*:\s*)?`)
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
	namePattern := strings.Join(escapeDirectiveNames([]string{"model"}), "|")
	re := regexp.MustCompile(`(?i)(?:^|\s)/(?:` + namePattern + `)(?:$|\s|:)`)
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
