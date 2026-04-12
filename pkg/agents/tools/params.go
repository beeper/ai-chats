package tools

import (
	"fmt"

	"github.com/beeper/agentremote/pkg/shared/maputil"
)

// ReadString reads a string parameter from input.
// When required is true and the key is missing or not a string, returns an error.
func ReadString(params map[string]any, key string, required bool) (string, error) {
	s := maputil.StringArg(params, key)
	if s != "" {
		return s, nil
	}
	if !required {
		return "", nil
	}
	if _, ok := params[key]; !ok || params[key] == nil {
		return "", fmt.Errorf("parameter %q is required", key)
	}
	return "", fmt.Errorf("parameter %q must be a string", key)
}

// ReadStringDefault reads a string parameter with a default value.
func ReadStringDefault(params map[string]any, key, defaultVal string) string {
	return maputil.StringArgDefault(params, key, defaultVal)
}

// ReadNumber reads a numeric parameter from input.
func ReadNumber(params map[string]any, key string, required bool) (float64, error) {
	if v, ok := maputil.NumberArg(params, key); ok {
		return v, nil
	}
	if !required {
		return 0, nil
	}
	if _, ok := params[key]; !ok || params[key] == nil {
		return 0, fmt.Errorf("parameter %q is required", key)
	}
	return 0, fmt.Errorf("parameter %q must be a number", key)
}

// ReadInt reads an integer parameter from input.
func ReadInt(params map[string]any, key string, required bool) (int, error) {
	n, err := ReadNumber(params, key, required)
	return int(n), err
}

// ReadIntDefault reads an integer parameter with a default value.
func ReadIntDefault(params map[string]any, key string, defaultVal int) int {
	return maputil.IntArgDefault(params, key, defaultVal)
}

// ReadBool reads a boolean parameter from input.
func ReadBool(params map[string]any, key string, defaultVal bool) bool {
	return maputil.BoolArg(params, key, defaultVal)
}

// ReadStringSlice reads a string array parameter from input.
func ReadStringSlice(params map[string]any, key string, required bool) ([]string, error) {
	arr := maputil.StringSliceArg(params, key)
	if arr != nil {
		return arr, nil
	}
	if required {
		return nil, fmt.Errorf("parameter %q is required", key)
	}
	return nil, nil
}

// ReadStringArray reads a string array parameter, returning nil if not present.
func ReadStringArray(params map[string]any, key string) []string {
	return maputil.StringSliceArg(params, key)
}

// ReadMap reads a map parameter from input.
func ReadMap(params map[string]any, key string, required bool) (map[string]any, error) {
	m := maputil.MapArg(params, key)
	if m != nil {
		return m, nil
	}
	if required {
		if _, ok := params[key]; !ok || params[key] == nil {
			return nil, fmt.Errorf("parameter %q is required", key)
		}
		return nil, fmt.Errorf("parameter %q must be an object", key)
	}
	return nil, nil
}
