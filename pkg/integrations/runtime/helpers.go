package runtime

import (
	"strings"
)

// ModuleOrNil returns nil when the host is absent, otherwise it constructs the module.
func ModuleOrNil[T ModuleHooks](host Host, newFn func(Host) T) T {
	var zero T
	if host == nil {
		return zero
	}
	return newFn(host)
}

// MatchesName compares names case-insensitively after trimming whitespace.
func MatchesName(actual, expected string) bool {
	return strings.EqualFold(strings.TrimSpace(actual), strings.TrimSpace(expected))
}

// MatchesAnyName compares against a small list of allowed names.
func MatchesAnyName(actual string, expected ...string) bool {
	for _, name := range expected {
		if MatchesName(actual, name) {
			return true
		}
	}
	return false
}
