package maputil

// StringSliceArg extracts a string slice from a map[string]any by key.
// Handles []string, []any (extracting string elements), and single string values.
// Returns nil if the key is missing or the value is not convertible.
func StringSliceArg(args map[string]any, key string) []string {
	v, ok := args[key]
	if !ok || v == nil {
		return nil
	}
	switch arr := v.(type) {
	case []string:
		return arr
	case []any:
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{arr}
	}
	return nil
}

// MapArg extracts a map[string]any value from a map[string]any by key.
// Returns nil if the key is missing or the value is not a map.
func MapArg(args map[string]any, key string) map[string]any {
	v, ok := args[key]
	if !ok || v == nil {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return m
}
