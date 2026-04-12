package opencode

import (
	"net/url"
	"strings"

	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote/pkg/shared/stringutil"
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
