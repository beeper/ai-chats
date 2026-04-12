package openclaw

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote/pkg/shared/openclawconv"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

const openClawGhostIDPrefixV1 = "v1:openclaw-agent:"

func openClawGatewayID(gatewayURL, label string) string {
	key := strings.ToLower(strings.TrimSpace(gatewayURL)) + "|" + strings.ToLower(strings.TrimSpace(label))
	return stringutil.ShortHash(key, 8)
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
	return networkid.UserID(openClawGhostIDPrefixV1 +
		base64.RawURLEncoding.EncodeToString([]byte(string(loginID))) + ":" +
		base64.RawURLEncoding.EncodeToString([]byte(trimmed)))
}

func openClawGhostUserID(agentID string) networkid.UserID {
	trimmed := openclawconv.CanonicalAgentID(agentID)
	if trimmed == "" {
		trimmed = "gateway"
	}
	return networkid.UserID(openClawGhostIDPrefixV1 + base64.RawURLEncoding.EncodeToString([]byte(trimmed)))
}

func parseOpenClawGhostID(ghostID string) (loginID networkid.UserLoginID, agentID string, ok bool) {
	trimmed := strings.TrimSpace(ghostID)
	if suffix, ok := strings.CutPrefix(trimmed, openClawGhostIDPrefixV1); ok {
		parts := strings.SplitN(suffix, ":", 2)
		decode := func(raw string) (string, bool) {
			data, err := base64.RawURLEncoding.DecodeString(raw)
			if err != nil {
				return "", false
			}
			return strings.TrimSpace(string(data)), true
		}
		switch len(parts) {
		case 1:
			agent, ok := decode(parts[0])
			if !ok {
				return "", "", false
			}
			agent = openclawconv.CanonicalAgentID(agent)
			if agent == "" {
				return "", "", false
			}
			return "", agent, true
		case 2:
			login, ok := decode(parts[0])
			if !ok {
				return "", "", false
			}
			agent, ok := decode(parts[1])
			if !ok {
				return "", "", false
			}
			agent = openclawconv.CanonicalAgentID(agent)
			if login == "" || agent == "" {
				return "", "", false
			}
			return networkid.UserLoginID(login), agent, true
		default:
			return "", "", false
		}
	}
	suffix, ok := strings.CutPrefix(trimmed, "openclaw-agent:")
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
