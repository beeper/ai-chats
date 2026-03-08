package connector

import (
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

func baseLoginID(providerSlug string, mxid id.UserID) networkid.UserLoginID {
	return networkid.UserLoginID(fmt.Sprintf("%s:%s", strings.TrimSpace(providerSlug), url.PathEscape(string(mxid))))
}

func nthLoginID(providerSlug string, mxid id.UserID, ordinal int) networkid.UserLoginID {
	base := baseLoginID(providerSlug, mxid)
	if ordinal <= 1 {
		return base
	}
	return networkid.UserLoginID(fmt.Sprintf("%s:%d", base, ordinal))
}

func providerLoginID(provider string, mxid id.UserID, ordinal int) networkid.UserLoginID {
	return nthLoginID(providerSlug(provider), mxid, ordinal)
}

func managedBeeperLoginID(mxid id.UserID) networkid.UserLoginID {
	return baseLoginID("beeper", mxid)
}

func providerSlug(provider string) string {
	switch strings.TrimSpace(provider) {
	case ProviderBeeper:
		return "beeper"
	case ProviderOpenAI:
		return "openai"
	case ProviderOpenRouter:
		return "openrouter"
	case ProviderMagicProxy:
		return "magic-proxy"
	default:
		return strings.TrimSpace(provider)
	}
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

const (
	ResolvedTargetUnknown = ""
	ResolvedTargetModel   = "model"
	ResolvedTargetAgent   = "agent"
)

type ResolvedTarget struct {
	Kind    string
	GhostID networkid.UserID
	ModelID string
	AgentID string
}

func resolveTargetFromGhostID(ghostID networkid.UserID) *ResolvedTarget {
	if ghostID == "" {
		return nil
	}
	if modelID := strings.TrimSpace(parseModelFromGhostID(string(ghostID))); modelID != "" {
		return &ResolvedTarget{
			Kind:    ResolvedTargetModel,
			GhostID: ghostID,
			ModelID: modelID,
		}
	}
	if agentID, ok := parseAgentFromGhostID(string(ghostID)); ok && strings.TrimSpace(agentID) != "" {
		return &ResolvedTarget{
			Kind:    ResolvedTargetAgent,
			GhostID: ghostID,
			AgentID: strings.TrimSpace(agentID),
		}
	}
	return nil
}

func portalMeta(portal *bridgev2.Portal) *PortalMetadata {
	meta := bridgeadapter.EnsurePortalMetadata[PortalMetadata](portal)
	if meta != nil {
		meta.ResolvedTarget = resolveTargetFromGhostID(portal.OtherUserID)
	}
	return meta
}

func resolveAgentID(meta *PortalMetadata) string {
	if meta == nil || meta.ResolvedTarget == nil {
		return ""
	}
	return meta.ResolvedTarget.AgentID
}

func messageMeta(msg *database.Message) *MessageMetadata {
	if msg == nil || msg.Metadata == nil {
		return nil
	}
	return msg.Metadata.(*MessageMetadata)
}

// Filters out non-conversation messages and messages explicitly excluded
// (e.g., welcome messages).
func shouldIncludeInHistory(meta *MessageMetadata) bool {
	if meta == nil {
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
	return len(meta.CanonicalUIMessage) > 0 ||
		strings.TrimSpace(meta.Body) != "" ||
		len(meta.ToolCalls) > 0 ||
		strings.TrimSpace(meta.MediaURL) != "" ||
		len(meta.GeneratedFiles) > 0
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
