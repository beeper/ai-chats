package opencodebridge

import "strings"

func traceLevel(meta *PortalMeta) string {
	if meta == nil {
		return "off"
	}
	level := strings.ToLower(strings.TrimSpace(meta.VerboseLevel))
	switch level {
	case "on", "full", "off":
		return level
	default:
		if level == "" {
			return "off"
		}
		return level
	}
}

func traceEnabled(meta *PortalMeta) bool {
	level := traceLevel(meta)
	return level == "on" || level == "full"
}

func traceFull(meta *PortalMeta) bool {
	return traceLevel(meta) == "full"
}
