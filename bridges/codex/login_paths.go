package codex

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/xid"
)

func (cl *CodexLogin) resolveCodexCommand() string {
	if cl.Connector == nil {
		return "codex"
	}
	return resolveCodexCommandFromConfig(cl.Connector.Config.Codex)
}

func (cl *CodexLogin) resolveCodexHomeBaseDir() string {
	var base string
	if cl.Connector != nil && cl.Connector.Config.Codex != nil {
		base = strings.TrimSpace(cl.Connector.Config.Codex.HomeBaseDir)
	}
	if base == "" {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			base = filepath.Join(home, ".local", "share", "agentremote", "codex")
		} else {
			base = filepath.Join(os.TempDir(), "agentremote-codex")
		}
	}
	base = strings.TrimSpace(base)
	if rest, isTilde := strings.CutPrefix(base, "~"); isTilde && (rest == "" || rest[0] == '/') {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			base = filepath.Join(home, rest)
		}
	}
	if abs, err := filepath.Abs(base); err == nil {
		return abs
	}
	return base
}

func generateShortID() string {
	return xid.New().String()
}
