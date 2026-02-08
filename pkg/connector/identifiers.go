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
)

// makeUserLoginID creates a stable login ID for a provider+API key pair.
// Use makeUserLoginIDWithSuffix to disambiguate duplicates of the same config.
func makeUserLoginID(mxid id.UserID, provider, apiKey string) networkid.UserLoginID {
	return makeUserLoginIDWithSuffix(mxid, provider, apiKey, "")
}

// makeUserLoginIDWithSuffix creates a login ID with an extra suffix for duplicate accounts.
// The suffix is appended verbatim and should be URL-safe.
func makeUserLoginIDWithSuffix(mxid id.UserID, provider, apiKey, suffix string) networkid.UserLoginID {
	escaped := url.PathEscape(string(mxid))
	// Hash the API key to create unique but stable identifier per account
	keyHash := sha256.Sum256([]byte(apiKey))
	keyHashShort := hex.EncodeToString(keyHash[:8]) // First 8 bytes = 16 hex chars
	base := fmt.Sprintf("openai:%s:%s:%s", escaped, provider, keyHashShort)
	if suffix == "" {
		return networkid.UserLoginID(base)
	}
	return networkid.UserLoginID(fmt.Sprintf("%s:%s", base, suffix))
}

func makeCodexUserLoginID(mxid id.UserID, instanceID string) networkid.UserLoginID {
	escaped := url.PathEscape(string(mxid))
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		instanceID = xid.New().String()
	}
	return networkid.UserLoginID(fmt.Sprintf("codex:%s:%s", escaped, instanceID))
}

func portalKeyForChat(loginID networkid.UserLoginID) networkid.PortalKey {
	return networkid.PortalKey{
		ID:       networkid.PortalID(fmt.Sprintf("openai:%s:%s", loginID, xid.New().String())),
		Receiver: loginID,
	}
}

func defaultCodexChatPortalKey(loginID networkid.UserLoginID) networkid.PortalKey {
	return networkid.PortalKey{
		ID:       networkid.PortalID(fmt.Sprintf("codex:%s:default-chat", loginID)),
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
	return networkid.UserID(fmt.Sprintf("openai-user:%s", loginID))
}

func portalMeta(portal *bridgev2.Portal) *PortalMetadata {
	return portal.Metadata.(*PortalMetadata)
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
// Filters out commands (messages starting with /), non-conversation messages,
// and messages explicitly excluded (e.g., welcome messages).
func shouldIncludeInHistory(meta *MessageMetadata) bool {
	if meta == nil || meta.Body == "" {
		return false
	}
	// Skip messages explicitly excluded (welcome messages, etc.)
	if meta.ExcludeFromHistory {
		return false
	}
	// Skip command messages
	if strings.HasPrefix(meta.Body, "/") {
		return false
	}
	// Only include user and assistant messages
	if meta.Role != "user" && meta.Role != "assistant" {
		return false
	}
	return true
}

func loginMetadata(login *bridgev2.UserLogin) *UserLoginMetadata {
	if login == nil {
		return &UserLoginMetadata{}
	}
	if login.Metadata == nil {
		meta := &UserLoginMetadata{}
		login.Metadata = meta
		return meta
	}
	meta, ok := login.Metadata.(*UserLoginMetadata)
	if !ok || meta == nil {
		// Don't crash on schema mismatches; allow logout/deletion to proceed.
		meta = &UserLoginMetadata{}
		login.Metadata = meta
	}
	return meta
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

// MakeMessageID creates a message ID from a Matrix event ID
func MakeMessageID(eventID id.EventID) networkid.MessageID {
	return networkid.MessageID(fmt.Sprintf("mx:%s", string(eventID)))
}

// cronPortalKey creates a deterministic portal key for a cron job room.
// Format: "openai:{loginID}:cron:{agentID}:{jobID}"
func cronPortalKey(loginID networkid.UserLoginID, agentID, jobID string) networkid.PortalKey {
	return networkid.PortalKey{
		ID:       networkid.PortalID(fmt.Sprintf("openai:%s:cron:%s:%s", loginID, url.PathEscape(agentID), url.PathEscape(jobID))),
		Receiver: loginID,
	}
}
