package opencodebridge

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"

	"maunium.net/go/mautrix/bridgev2/networkid"
)

func OpenCodeInstanceID(baseURL, username string) string {
	key := strings.ToLower(strings.TrimSpace(baseURL)) + "|" + strings.ToLower(strings.TrimSpace(username))
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:8])
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
