package maputil

import "strings"

// BoolArg extracts a boolean value from a map[string]any by key.
// Handles bool, string ("true"/"1"/"yes"), float64, and int types.
// Returns defaultVal if the key is missing or the value is not convertible.
func BoolArg(args map[string]any, key string, defaultVal bool) bool {
	v, ok := args[key]
	if !ok {
		return defaultVal
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		lower := strings.ToLower(strings.TrimSpace(b))
		return lower == "true" || lower == "1" || lower == "yes"
	case float64:
		return b != 0
	case int:
		return b != 0
	}
	return defaultVal
}
