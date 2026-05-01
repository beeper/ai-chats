package tools

import (
	"fmt"
	"strings"

	"github.com/beeper/agentremote/pkg/shared/maputil"
)

// ReadString reads a string parameter from input.
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
func ReadNumber(params map[string]any, key string, required bool) (float64, error) {
	if v, ok := maputil.NumberArg(params, key); ok {
		return v, nil
	}
	if !required {
		return 0, nil
	}
	if _, exists := params[key]; !exists || params[key] == nil {
		return 0, fmt.Errorf("parameter %q is required", key)
	}
	return 0, fmt.Errorf("parameter %q must be a number", key)
}

// ReadInt reads an integer parameter from input.
func ReadInt(params map[string]any, key string, required bool) (int, error) {
	n, err := ReadNumber(params, key, required)
	if err != nil {
		return 0, err
	}
	return int(n), nil
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
		return []string{arr}, nil
	}
	if required {
		return nil, fmt.Errorf("parameter %q must be a string array", key)
	}
	return nil, nil
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
