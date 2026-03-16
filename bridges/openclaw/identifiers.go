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

func openClawGhostUserID(agentID string) networkid.UserID {
	trimmed := canonicalOpenClawAgentID(agentID)
	if trimmed == "" {
		trimmed = "gateway"
	}
	return networkid.UserID("openclaw-agent:" + url.PathEscape(trimmed))
}

func parseOpenClawGhostID(ghostID string) (string, bool) {
	suffix, ok := strings.CutPrefix(strings.TrimSpace(ghostID), "openclaw-agent:")
	if !ok {
		return "", false
	}
	value, err := url.PathUnescape(suffix)
	if err != nil {
		return "", false
	}
	value = canonicalOpenClawAgentID(value)
	if value == "" {
		return "", false
	}
	return value, true
}

func openClawDMAgentSessionKey(agentID string) string {
	agentID = canonicalOpenClawAgentID(agentID)
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

func canonicalOpenClawAgentID(agentID string) string {
	return openclawconv.CanonicalAgentID(agentID)
}
