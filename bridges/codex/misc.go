package codex

import (
	"strings"
)

const aiCapabilityID = "com.beeper.ai.v1"

func normalizeToolAlias(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}
