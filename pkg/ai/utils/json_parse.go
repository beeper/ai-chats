package utils

import "encoding/json"

// ParseStreamingJSON attempts to decode possibly incomplete JSON.
// If parsing fails it best-effort trims incomplete suffixes and retries,
// returning an empty object when no parse can succeed.
func ParseStreamingJSON(partial string) map[string]any {
	if partial == "" {
		return map[string]any{}
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(partial), &out); err == nil && out != nil {
		return out
	}

	// Best-effort fallback: trim the tail and try to parse repeatedly.
	for i := len(partial) - 1; i > 1; i-- {
		ch := partial[i]
		switch ch {
		case '{', '[', ',', ':':
			continue
		}
		candidate := partial[:i]
		out = nil
		if err := json.Unmarshal([]byte(candidate), &out); err == nil && out != nil {
			return out
		}
	}
	return map[string]any{}
}
