package connector

import "strings"

func traceLevel(meta *PortalMetadata) string {
	if meta == nil {
		return "off"
	}
	if level, ok := normalizeVerboseLevel(meta.VerboseLevel); ok {
		return level
	}
	level := strings.ToLower(strings.TrimSpace(meta.VerboseLevel))
	if level == "" {
		return "off"
	}
	return level
}

func traceEnabled(meta *PortalMetadata) bool {
	level := traceLevel(meta)
	return level == "on" || level == "full"
}

func traceFull(meta *PortalMetadata) bool {
	return traceLevel(meta) == "full"
}
