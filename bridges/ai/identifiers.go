package ai

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/rs/xid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-chats/sdk"
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

func providerSlug(provider string) string {
	switch strings.TrimSpace(provider) {
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

func modelUserID(modelID string) networkid.UserID {
	// Convert "gpt-4.1" to "model-gpt-4.1"
	return networkid.UserID(fmt.Sprintf("model-%s", url.PathEscape(modelID)))
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

func humanUserID(loginID networkid.UserLoginID) networkid.UserID {
	return sdk.HumanUserID("openai-user", loginID)
}

const (
	ResolvedTargetUnknown = ""
	ResolvedTargetModel   = "model"
)

type ResolvedTarget struct {
	Kind    string
	GhostID networkid.UserID
	ModelID string
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
	return nil
}

func portalMeta(portal *bridgev2.Portal) *PortalMetadata {
	meta := sdk.EnsurePortalMetadata[PortalMetadata](portal)
	if meta != nil && portal != nil {
		meta.ResolvedTarget = resolveTargetFromGhostID(portal.OtherUserID)
	}
	return meta
}

func setPortalResolvedTarget(portal *bridgev2.Portal, meta *PortalMetadata, ghostID networkid.UserID) {
	if portal == nil {
		return
	}
	portal.OtherUserID = ghostID
	if meta == nil {
		meta = portalMeta(portal)
	}
	if meta != nil {
		meta.ResolvedTarget = resolveTargetFromGhostID(ghostID)
	}
}

func messageMeta(msg *database.Message) *MessageMetadata {
	if msg == nil || msg.Metadata == nil {
		return nil
	}
	return msg.Metadata.(*MessageMetadata)
}

// Filters out non-conversation messages and messages explicitly excluded
// (e.g. welcome notices).
func shouldIncludeInHistory(meta *MessageMetadata) bool {
	if meta == nil {
		return false
	}
	if meta.ExcludeFromHistory {
		return false
	}
	if meta.Role != "user" && meta.Role != "assistant" {
		return false
	}
	return len(meta.CanonicalTurnData) > 0
}

func loginMetadata(login *bridgev2.UserLogin) *UserLoginMetadata {
	return sdk.EnsureLoginMetadata[UserLoginMetadata](login)
}

func formatChatSlug(index int) string {
	return fmt.Sprintf("chat-%d", index)
}
