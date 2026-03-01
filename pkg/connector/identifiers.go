package connector

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/rs/xid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

// makeUserLoginIDForConfig creates a stable login ID by hashing provider + base URL + API key.
//
// Multiple logins with identical config are supported by appending an ordinal suffix: -2, -3, ...
func makeUserLoginIDForConfig(mxid id.UserID, provider, apiKey, baseURL string, ordinal int) networkid.UserLoginID {
	escaped := url.PathEscape(string(mxid))
	provider = strings.TrimSpace(provider)
	apiKey = strings.TrimSpace(apiKey)
	baseURL = normalizeBaseURLForLoginID(baseURL)

	// Stable, unambiguous hash input.
	sum := sha256.Sum256([]byte(provider + "\n" + baseURL + "\n" + apiKey))
	hashShort := hex.EncodeToString(sum[:8]) // 16 hex chars

	base := fmt.Sprintf("openai:%s:%s:%s", escaped, provider, hashShort)
	if ordinal <= 1 {
		return networkid.UserLoginID(base)
	}
	return networkid.UserLoginID(fmt.Sprintf("%s-%d", base, ordinal))
}

func normalizeBaseURLForLoginID(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		return ""
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return strings.ToLower(baseURL)
	}
	parsed.Host = strings.ToLower(strings.TrimSpace(parsed.Host))
	parsed.Fragment = ""
	parsed.RawQuery = ""
	clean := strings.TrimSpace(parsed.String())
	clean = strings.TrimRight(clean, "/")
	return strings.ToLower(clean)
}

func portalKeyForChat(loginID networkid.UserLoginID) networkid.PortalKey {
	return networkid.PortalKey{
		ID:       networkid.PortalID(fmt.Sprintf("openai:%s:%s", loginID, xid.New().String())),
		Receiver: loginID,
	}
}

func defaultChatPortalKey(loginID networkid.UserLoginID) networkid.PortalKey {
	return networkid.PortalKey{
		ID:       networkid.PortalID(fmt.Sprintf("openai:%s:default-chat", loginID)),
		Receiver: loginID,
	}
}

func modelUserID(modelID string) networkid.UserID {
	// Convert "gpt-4o" to "model-gpt-4o"
	return networkid.UserID(fmt.Sprintf("model-%s", url.PathEscape(modelID)))
}

// agentUserID creates a ghost user ID for an agent (no model suffix).
// Format: "agent-{agent-id}"
func agentUserID(agentID string) networkid.UserID {
	return networkid.UserID(fmt.Sprintf("agent-%s", url.PathEscape(agentID)))
}

// parseModelFromGhostID extracts the model ID from a ghost ID (format: "model-{escaped-model-id}")
// Returns empty string if the ghost ID doesn't match the expected format.
func parseModelFromGhostID(ghostID string) string {
	if suffix, ok := strings.CutPrefix(ghostID, "model-"); ok {
		modelID, err := url.PathUnescape(suffix)
		if err == nil {
			return modelID
		}
	}
	return ""
}

// parseAgentFromGhostID extracts the agent ID from a ghost ID (format: "agent-{escaped-agent-id}").
// Returns empty string and false if the ghost ID is not an agent-only ghost.
func parseAgentFromGhostID(ghostID string) (agentID string, ok bool) {
	if strings.Contains(ghostID, ":model-") {
		return "", false
	}
	if suffix, hasPrefix := strings.CutPrefix(ghostID, "agent-"); hasPrefix {
		agentID, err := url.PathUnescape(suffix)
		if err == nil {
			return agentID, true
		}
	}
	return "", false
}

func humanUserID(loginID networkid.UserLoginID) networkid.UserID {
	return bridgeadapter.HumanUserID("openai-user", loginID)
}

func portalMeta(portal *bridgev2.Portal) *PortalMetadata {
	return bridgeadapter.EnsurePortalMetadata[PortalMetadata](portal)
}

// resolveAgentID returns the configured agent ID.
func resolveAgentID(meta *PortalMetadata) string {
	if meta == nil {
		return ""
	}
	return meta.AgentID
}

func messageMeta(msg *database.Message) *MessageMetadata {
	if msg == nil || msg.Metadata == nil {
		return nil
	}
	return msg.Metadata.(*MessageMetadata)
}

// shouldIncludeInHistory checks if a message should be included in LLM history.
// Filters out non-conversation messages and messages explicitly excluded
// (e.g., welcome messages).
func shouldIncludeInHistory(meta *MessageMetadata) bool {
	if meta == nil || meta.Body == "" {
		return false
	}
	// Skip messages explicitly excluded (welcome messages, etc.)
	if meta.ExcludeFromHistory {
		return false
	}
	// Only include user and assistant messages
	if meta.Role != "user" && meta.Role != "assistant" {
		return false
	}
	return true
}

func loginMetadata(login *bridgev2.UserLogin) *UserLoginMetadata {
	return bridgeadapter.EnsureLoginMetadata[UserLoginMetadata](login)
}

func formatChatSlug(index int) string {
	return fmt.Sprintf("chat-%d", index)
}

func parseChatSlug(slug string) (int, bool) {
	if suffix, ok := strings.CutPrefix(slug, "chat-"); ok {
		if idx, err := strconv.Atoi(suffix); err == nil {
			return idx, true
		}
	}
	return 0, false
}
