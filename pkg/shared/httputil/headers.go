package httputil

import "maps"

// MergeHeaders merges override headers into base, returning a new map.
func MergeHeaders(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := maps.Clone(base)
	if out == nil {
		out = make(map[string]string)
	}
	maps.Copy(out, override)
	return out
}
