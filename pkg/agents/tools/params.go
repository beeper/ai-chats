package tools

import (
	"fmt"
	"strconv"
	"strings"
)

// ReadString reads a string parameter from input.
// Following clawdbot's readStringParam pattern.
func ReadString(params map[string]any, key string, required bool) (string, error) {
	v, ok := params[key]
	if !ok || v == nil {
		if required {
			return "", fmt.Errorf("parameter %q is required", key)
		}
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		if required {
			return "", fmt.Errorf("parameter %q must be a string", key)
		}
		return "", nil
	}
	return strings.TrimSpace(s), nil
}

// ReadStringDefault reads a string parameter with a default value.
func ReadStringDefault(params map[string]any, key, defaultVal string) string {
	s, err := ReadString(params, key, false)
	if err != nil || s == "" {
		return defaultVal
	}
	return s
}

// ReadNumber reads a numeric parameter from input.
// Following clawdbot's readNumberParam pattern.
func ReadNumber(params map[string]any, key string, required bool) (float64, error) {
	v, ok := params[key]
	if !ok || v == nil {
		if required {
			return 0, fmt.Errorf("parameter %q is required", key)
		}
		return 0, nil
	}
	switch n := v.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case int32:
		return float64(n), nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			if required {
				return 0, fmt.Errorf("parameter %q must be a number", key)
			}
			return 0, nil
		}
		return f, nil
	}
	if required {
		return 0, fmt.Errorf("parameter %q must be a number", key)
	}
	return 0, nil
}

// ReadNumberDefault reads a numeric parameter with a default value.
func ReadNumberDefault(params map[string]any, key string, defaultVal float64) float64 {
	if _, ok := params[key]; !ok {
		return defaultVal
	}
	n, err := ReadNumber(params, key, false)
	if err != nil {
		return defaultVal
	}
	return n
}

// ReadInt reads an integer parameter from input.
func ReadInt(params map[string]any, key string, required bool) (int, error) {
	n, err := ReadNumber(params, key, required)
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// ReadIntDefault reads an integer parameter with a default value.
func ReadIntDefault(params map[string]any, key string, defaultVal int) int {
	if _, ok := params[key]; !ok {
		return defaultVal
	}
	n, err := ReadInt(params, key, false)
	if err != nil {
		return defaultVal
	}
	return n
}

// ReadBool reads a boolean parameter from input.
func ReadBool(params map[string]any, key string, defaultVal bool) bool {
	v, ok := params[key]
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

// ReadStringSlice reads a string array parameter from input.
func ReadStringSlice(params map[string]any, key string, required bool) ([]string, error) {
	v, ok := params[key]
	if !ok || v == nil {
		if required {
			return nil, fmt.Errorf("parameter %q is required", key)
		}
		return nil, nil
	}
	switch arr := v.(type) {
	case []string:
		return arr, nil
	case []any:
		result := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result, nil
	case string:
		// Single string as slice
		return []string{arr}, nil
	}
	if required {
		return nil, fmt.Errorf("parameter %q must be a string array", key)
	}
	return nil, nil
}

// ReadStringArray reads a string array parameter, returning nil if not present.
// Convenience wrapper around ReadStringSlice that ignores errors.
func ReadStringArray(params map[string]any, key string) []string {
	arr, _ := ReadStringSlice(params, key, false)
	return arr
}

// ReadMap reads a map parameter from input.
func ReadMap(params map[string]any, key string, required bool) (map[string]any, error) {
	v, ok := params[key]
	if !ok || v == nil {
		if required {
			return nil, fmt.Errorf("parameter %q is required", key)
		}
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		if required {
			return nil, fmt.Errorf("parameter %q must be an object", key)
		}
		return nil, nil
	}
	return m, nil
}
