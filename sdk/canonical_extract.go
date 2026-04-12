package sdk

import "github.com/beeper/agentremote/pkg/shared/jsonutil"

// NormalizeUIParts coerces a raw parts value (which may be []any or
// []map[string]any) into a typed []map[string]any slice.
func NormalizeUIParts(raw any) []map[string]any {
	switch typed := raw.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			part := jsonutil.ToMap(item)
			if len(part) == 0 {
				continue
			}
			out = append(out, part)
		}
		return out
	default:
		return nil
	}
}
