package codex

import (
	"strings"

	"github.com/rs/xid"
)

func generateShortID() string {
	return xid.New().String()
}

func isCodexIdentifier(identifier string) bool {
	switch strings.ToLower(strings.TrimSpace(identifier)) {
	case "codex", "@codex", "codex:default", "codex:codex":
		return true
	default:
		return false
	}
}
