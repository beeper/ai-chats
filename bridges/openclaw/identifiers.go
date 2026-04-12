package openclaw

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"

	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote/pkg/shared/openclawconv"
)

func openClawGatewayID(gatewayURL, label string) string {
	key := strings.ToLower(strings.TrimSpace(gatewayURL)) + "|" + strings.ToLower(strings.TrimSpace(label))
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

func openClawPortalKey(loginID networkid.UserLoginID, gatewayID, sessionKey string) networkid.PortalKey {
	return networkid.PortalKey{
		ID: networkid.PortalID(
			"openclaw:" +
				string(loginID) + ":" +
				url.PathEscape(strings.TrimSpace(gatewayID)) + ":" +
				url.PathEscape(strings.TrimSpace(sessionKey)),
		),
		Receiver: loginID,
	}
}

func openClawScopedGhostUserID(loginID networkid.UserLoginID, agentID string) networkid.UserID {
	trimmed := openclawconv.CanonicalAgentID(agentID)
	if trimmed == "" {
		trimmed = "gateway"
	}
	return networkid.UserID("openclaw-agent:" + url.PathEscape(string(loginID)) + ":" + url.PathEscape(trimmed))
}

func openClawGhostUserID(agentID string) networkid.UserID {
	trimmed := openclawconv.CanonicalAgentID(agentID)
	if trimmed == "" {
		trimmed = "gateway"
	}
	return networkid.UserID("openclaw-agent:" + url.PathEscape(trimmed))
}

func parseOpenClawGhostID(ghostID string) (loginID networkid.UserLoginID, agentID string, ok bool) {
	suffix, ok := strings.CutPrefix(strings.TrimSpace(ghostID), "openclaw-agent:")
	if !ok {
		return "", "", false
	}
	parts := strings.SplitN(suffix, ":", 2)
	value := suffix
	if len(parts) == 2 {
		login, err := url.PathUnescape(parts[0])
		if err != nil {
			return "", "", false
		}
		loginID = networkid.UserLoginID(strings.TrimSpace(login))
		value = parts[1]
	}
	value, err := url.PathUnescape(value)
	if err != nil {
		return "", "", false
	}
	value = openclawconv.CanonicalAgentID(value)
	if value == "" {
		return "", "", false
	}
	return loginID, value, true
}

func openClawDMAgentSessionKey(agentID string) string {
	agentID = openclawconv.CanonicalAgentID(agentID)
	if agentID == "" {
		agentID = "gateway"
	}
	return fmt.Sprintf("agent:%s:matrix-dm", agentID)
}

func isOpenClawSyntheticDMSessionKey(sessionKey string) bool {
	sessionKey = strings.ToLower(strings.TrimSpace(sessionKey))
	if !strings.HasSuffix(sessionKey, ":matrix-dm") {
		return false
	}
	return openclawconv.AgentIDFromSessionKey(sessionKey) != ""
}
