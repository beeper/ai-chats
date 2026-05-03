package maputil

import (
	"encoding/json"
	"strconv"
	"strings"
)

// NumberArg extracts a numeric value from a map[string]any by key.
// Handles float64, float32, int, int64, int32, uint, uint64, uint32,
// json.Number, and string (parsed as float64).
// Returns (0, false) if the key is missing, nil, or not convertible to a number.
func NumberArg(args map[string]any, key string) (float64, bool) {
	if args == nil {
		return 0, false
	}
	raw, ok := args[key]
	if !ok || raw == nil {
		return 0, false
	}
	switch v := raw.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint64:
		return float64(v), true
	case uint32:
		return float64(v), true
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f, true
		}
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// IntArg extracts an integer value from a map[string]any by key.
// Uses NumberArg internally and truncates to int.
func IntArg(args map[string]any, key string) (int, bool) {
	v, ok := NumberArg(args, key)
	if !ok {
		return 0, false
	}
	return int(v), true
}
