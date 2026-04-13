package opencode

import (
	"net/url"
	"path/filepath"
	"strings"

	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

const (
	OpenCodeModeRemote          = "remote"
	OpenCodeModeManagedLauncher = "managed_launcher"
	OpenCodeModeManaged         = "managed"
)

func OpenCodeInstanceID(baseURL, username string) string {
	key := strings.ToLower(strings.TrimSpace(baseURL)) + "|" + strings.ToLower(strings.TrimSpace(username))
	return stringutil.ShortHash(key, 8)
}

func OpenCodeManagedLauncherID(parts ...string) string {
	key := "managed-launcher"
	for _, part := range parts {
		key += "|" + strings.TrimSpace(part)
	}
	return stringutil.ShortHash(key, 8)
}

func OpenCodeManagedInstanceID(loginID, directory string) string {
	return stringutil.ShortHash("managed|"+strings.TrimSpace(loginID)+"|"+strings.TrimSpace(directory), 8)
}

func OpenCodeUserID(instanceID string) networkid.UserID {
	return networkid.UserID("opencode-" + url.PathEscape(instanceID))
}

func ParseOpenCodeGhostID(ghostID string) (string, bool) {
	if suffix, ok := strings.CutPrefix(ghostID, "opencode-"); ok {
		if value, err := url.PathUnescape(suffix); err == nil {
			return value, true
		}
	}
	return "", false
}

func ParseOpenCodeIdentifier(identifier string) (string, bool) {
	trimmed := strings.TrimSpace(identifier)
	if trimmed == "" {
		return "", false
	}
	if value, ok := strings.CutPrefix(trimmed, "opencode:"); ok {
		value = strings.TrimSpace(value)
		if value != "" {
			return value, true
		}
	}
	if value, ok := ParseOpenCodeGhostID(trimmed); ok {
		return value, true
	}
	return "", false
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

func OpenCodePortalKey(loginID networkid.UserLoginID, instanceID, sessionID string) networkid.PortalKey {
	return networkid.PortalKey{
		ID: networkid.PortalID(
			"opencode:" +
				string(loginID) + ":" +
				url.PathEscape(instanceID) + ":" +
				url.PathEscape(sessionID),
		),
		Receiver: loginID,
	}
}
