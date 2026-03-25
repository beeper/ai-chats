package globmatch

import (
	"regexp"
	"strings"
)

// Pattern is a compiled glob-style pattern for tool name matching.
// It supports three forms: exact (literal equality), all (* wildcard),
// and regex (* expanded to .* via regexp.QuoteMeta).
type Pattern struct {
	kind  string
	value string
	re    *regexp.Regexp
}

// Compile compiles a single glob pattern. Empty patterns produce an
// exact match on "". The "*" pattern matches everything. Patterns
// containing "*" are converted to regexps; if the regexp fails to
// compile, the pattern degrades to exact match.
func Compile(pattern string) Pattern {
	normalized := strings.TrimSpace(strings.ToLower(pattern))
	if normalized == "" {
		return Pattern{kind: "exact", value: ""}
	}
	if normalized == "*" {
		return Pattern{kind: "all"}
	}
	if !strings.Contains(normalized, "*") {
		return Pattern{kind: "exact", value: normalized}
	}
	escaped := regexp.QuoteMeta(normalized)
	rePattern := "^" + strings.ReplaceAll(escaped, "\\*", ".*") + "$"
	re, err := regexp.Compile(rePattern)
	if err != nil {
		return Pattern{kind: "exact", value: normalized}
	}
	return Pattern{kind: "regex", re: re}
}

// CompileAll compiles a slice of glob patterns, skipping empty results.
func CompileAll(patterns []string) []Pattern {
	if len(patterns) == 0 {
		return nil
	}
	compiled := make([]Pattern, 0, len(patterns))
	for _, p := range patterns {
		entry := Compile(p)
		if entry.kind == "exact" && entry.value == "" {
			continue
		}
		compiled = append(compiled, entry)
	}
	return compiled
}

// Matches reports whether the pattern matches the given name.
func (p Pattern) Matches(name string) bool {
	switch p.kind {
	case "all":
		return true
	case "exact":
		return name == p.value
	case "regex":
		return p.re != nil && p.re.MatchString(name)
	}
	return false
}

// MatchesAny reports whether name matches any of the compiled patterns.
func MatchesAny(name string, patterns []Pattern) bool {
	for _, p := range patterns {
		if p.Matches(name) {
			return true
		}
	}
	return false
}

// MakePredicate builds a deny-wins predicate: if the name matches any
// deny pattern it returns false; otherwise it returns true when there
// are no allow patterns or the name matches at least one allow pattern.
func MakePredicate(allow, deny []Pattern) func(string) bool {
	return func(name string) bool {
		normalized := strings.TrimSpace(strings.ToLower(name))
		if MatchesAny(normalized, deny) {
			return false
		}
		return len(allow) == 0 || MatchesAny(normalized, allow)
	}
}
