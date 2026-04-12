package maputil

import (
	"fmt"
	"strings"
)

// StringArg extracts a trimmed string value from a map[string]any by key.
// Returns "" if the key is missing, nil, or not a string/fmt.Stringer.
func StringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	raw := args[key]
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return ""
	}
}

// StringArgDefault extracts a trimmed string value, returning defaultVal if the
// key is missing, nil, empty, or not a string.
func StringArgDefault(args map[string]any, key, defaultVal string) string {
	s := StringArg(args, key)
	if s == "" {
		return defaultVal
	}
	return s
}

// StringArgMulti tries multiple keys in order, returning the first non-empty
// trimmed string value and true. Returns ("", false) if none match.
func StringArgMulti(args map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		if s := StringArg(args, key); s != "" {
			return s, true
		}
	}
	return "", false
}
