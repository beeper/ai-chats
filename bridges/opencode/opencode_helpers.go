package opencode

import (
	"net/url"
	"path/filepath"
	"strings"

	"github.com/beeper/agentremote/bridges/opencode/api"
)

const (
	OpenCodeModeRemote          = "remote"
	OpenCodeModeManagedLauncher = "managed_launcher"
	OpenCodeModeManaged         = "managed"
)

func fillPartIDs(part *api.Part, msgID, sessionID string) {
	if part.MessageID == "" {
		part.MessageID = msgID
	}
	if part.SessionID == "" {
		part.SessionID = sessionID
	}
}

func (b *Bridge) InstanceConfig(instanceID string) *OpenCodeInstance {
	if b == nil || b.host == nil {
		return nil
	}
	meta := b.host.OpenCodeInstances()
	if meta == nil {
		return nil
	}
	return meta[instanceID]
}

func (b *Bridge) DisplayName(instanceID string) string {
	if b == nil {
		return ""
	}
	cfg := b.InstanceConfig(instanceID)
	return opencodeLabelFromURL(cfg)
}

func opencodeLabelFromURL(cfg *OpenCodeInstance) string {
	label := "OpenCode"
	if cfg == nil {
		return label
	}
	switch cfg.Mode {
	case OpenCodeModeManagedLauncher:
		return "Managed OpenCode"
	case OpenCodeModeManaged:
		dir := strings.TrimSpace(cfg.WorkingDirectory)
		if dir == "" {
			dir = strings.TrimSpace(cfg.DefaultDirectory)
		}
		if dir == "" {
			return "Managed OpenCode"
		}
		base := filepath.Base(dir)
		if base == "." || base == string(filepath.Separator) || base == "" {
			return "Managed OpenCode"
		}
		return "OpenCode (" + base + ")"
	}
	raw := strings.TrimSpace(cfg.URL)
	if raw == "" {
		return label
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return label
	}
	host := strings.TrimSpace(parsed.Host)
	if host == "" {
		host = strings.TrimSpace(parsed.Path)
	}
	if host == "" {
		return label
	}
	return label + " (" + host + ")"
}
