package connector

import (
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

func traceLevel(meta *PortalMetadata) string {
	if meta == nil {
		return "off"
	}
	if level, ok := stringutil.NormalizeEnum(meta.VerboseLevel, verboseLevelAliases); ok {
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
