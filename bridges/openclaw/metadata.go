package openclaw

import (
	"encoding/json"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote"
)

type UserLoginMetadata struct {
	Provider        string `json:"provider,omitempty"`
	GatewayURL      string `json:"gateway_url,omitempty"`
	AuthMode        string `json:"auth_mode,omitempty"`
	GatewayToken    string `json:"gateway_token,omitempty"`
	GatewayPassword string `json:"gateway_password,omitempty"`
	GatewayLabel    string `json:"gateway_label,omitempty"`
	DeviceToken     string `json:"device_token,omitempty"`
	SessionsSynced  bool   `json:"sessions_synced,omitempty"`
	LastSyncAt      int64  `json:"last_sync_at,omitempty"`
}

type PortalMetadata struct {
	IsOpenClawRoom               bool           `json:"is_openclaw_room,omitempty"`
	OpenClawGatewayID            string         `json:"openclaw_gateway_id,omitempty"`
	OpenClawSessionID            string         `json:"openclaw_session_id,omitempty"`
	OpenClawSessionKey           string         `json:"openclaw_session_key,omitempty"`
	OpenClawDMTargetAgentID      string         `json:"openclaw_dm_target_agent_id,omitempty"`
	OpenClawDMTargetAgentName    string         `json:"openclaw_dm_target_agent_name,omitempty"`
	OpenClawDMCreatedFromContact bool           `json:"openclaw_dm_created_from_contact,omitempty"`
	OpenClawSessionKind          string         `json:"openclaw_session_kind,omitempty"`
	OpenClawSessionLabel         string         `json:"openclaw_session_label,omitempty"`
	OpenClawDisplayName          string         `json:"openclaw_display_name,omitempty"`
	OpenClawDerivedTitle         string         `json:"openclaw_derived_title,omitempty"`
	OpenClawLastMessagePreview   string         `json:"openclaw_last_message_preview,omitempty"`
	OpenClawChannel              string         `json:"openclaw_channel,omitempty"`
	OpenClawSubject              string         `json:"openclaw_subject,omitempty"`
	OpenClawGroupChannel         string         `json:"openclaw_group_channel,omitempty"`
	OpenClawSpace                string         `json:"openclaw_space,omitempty"`
	OpenClawChatType             string         `json:"openclaw_chat_type,omitempty"`
	OpenClawOrigin               string         `json:"openclaw_origin,omitempty"`
	OpenClawAgentID              string         `json:"openclaw_agent_id,omitempty"`
	OpenClawSystemSent           bool           `json:"openclaw_system_sent,omitempty"`
	OpenClawAbortedLastRun       bool           `json:"openclaw_aborted_last_run,omitempty"`
	ThinkingLevel                string         `json:"thinking_level,omitempty"`
	VerboseLevel                 string         `json:"verbose_level,omitempty"`
	ReasoningLevel               string         `json:"reasoning_level,omitempty"`
	ElevatedLevel                string         `json:"elevated_level,omitempty"`
	SendPolicy                   string         `json:"send_policy,omitempty"`
	InputTokens                  int64          `json:"input_tokens,omitempty"`
	OutputTokens                 int64          `json:"output_tokens,omitempty"`
	TotalTokens                  int64          `json:"total_tokens,omitempty"`
	TotalTokensFresh             bool           `json:"total_tokens_fresh,omitempty"`
	ResponseUsage                string         `json:"response_usage,omitempty"`
	ModelProvider                string         `json:"model_provider,omitempty"`
	Model                        string         `json:"model,omitempty"`
	ContextTokens                int64          `json:"context_tokens,omitempty"`
	DeliveryContext              map[string]any `json:"delivery_context,omitempty"`
	LastChannel                  string         `json:"last_channel,omitempty"`
	LastTo                       string         `json:"last_to,omitempty"`
	LastAccountID                string         `json:"last_account_id,omitempty"`
	SessionUpdatedAt             int64          `json:"session_updated_at,omitempty"`
	OpenClawPreviewSnippet       string         `json:"openclaw_preview_snippet,omitempty"`
	OpenClawDefaultAgentID       string         `json:"openclaw_default_agent_id,omitempty"`
	OpenClawToolProfile          string         `json:"openclaw_tool_profile,omitempty"`
	OpenClawToolCount            int            `json:"openclaw_tool_count,omitempty"`
	OpenClawKnownModelCount      int            `json:"openclaw_known_model_count,omitempty"`
	OpenClawLastPreviewAt        int64          `json:"openclaw_last_preview_at,omitempty"`
	HistoryMode                  string         `json:"history_mode,omitempty"`
	RecentHistoryLimit           int            `json:"recent_history_limit,omitempty"`
	LastHistorySyncAt            int64          `json:"last_history_sync_at,omitempty"`
	LastTranscriptFingerprint    string         `json:"last_transcript_fingerprint,omitempty"`
	LastLiveSeq                  int64          `json:"last_live_seq,omitempty"`
}

type GhostMetadata struct {
	OpenClawAgentID        string `json:"openclaw_agent_id,omitempty"`
	OpenClawAgentName      string `json:"openclaw_agent_name,omitempty"`
	OpenClawAgentAvatarURL string `json:"openclaw_agent_avatar_url,omitempty"`
	OpenClawAgentEmoji     string `json:"openclaw_agent_emoji,omitempty"`
	OpenClawAgentRole      string `json:"openclaw_agent_role,omitempty"`
	LastSeenAt             int64  `json:"last_seen_at,omitempty"`
}

type MessageMetadata struct {
	agentremote.BaseMessageMetadata
	SessionID      string           `json:"session_id,omitempty"`
	SessionKey     string           `json:"session_key,omitempty"`
	RunID          string           `json:"run_id,omitempty"`
	ErrorText      string           `json:"error_text,omitempty"`
	TotalTokens    int64            `json:"total_tokens,omitempty"`
	Attachments    []map[string]any `json:"attachments,omitempty"`
	FirstTokenAtMs int64            `json:"first_token_at_ms,omitempty"`
}

func (mm *MessageMetadata) CopyFrom(other any) {
	src, ok := other.(*MessageMetadata)
	if !ok || src == nil {
		return
	}
	mm.BaseMessageMetadata.CopyFromBase(&src.BaseMessageMetadata)
	if src.SessionID != "" {
		mm.SessionID = src.SessionID
	}
	if src.SessionKey != "" {
		mm.SessionKey = src.SessionKey
	}
	if src.RunID != "" {
		mm.RunID = src.RunID
	}
	if src.ErrorText != "" {
		mm.ErrorText = src.ErrorText
	}
	if src.TotalTokens != 0 {
		mm.TotalTokens = src.TotalTokens
	}
	if len(src.Attachments) > 0 {
		mm.Attachments = src.Attachments
	}
	if src.FirstTokenAtMs != 0 {
		mm.FirstTokenAtMs = src.FirstTokenAtMs
	}
}

func loginMetadata(login *bridgev2.UserLogin) *UserLoginMetadata {
	return agentremote.EnsureLoginMetadata[UserLoginMetadata](login)
}

func portalMeta(portal *bridgev2.Portal) *PortalMetadata {
	return agentremote.EnsurePortalMetadata[PortalMetadata](portal)
}

func ghostMeta(ghost *bridgev2.Ghost) *GhostMetadata {
	if ghost == nil {
		return &GhostMetadata{}
	}
	switch typed := ghost.Metadata.(type) {
	case *GhostMetadata:
		if typed != nil {
			return typed
		}
	case map[string]any:
		data, err := json.Marshal(typed)
		if err == nil {
			var meta GhostMetadata
			if err = json.Unmarshal(data, &meta); err == nil {
				ghost.Metadata = &meta
				return &meta
			}
		}
	case map[string]string:
		data, err := json.Marshal(typed)
		if err == nil {
			var meta GhostMetadata
			if err = json.Unmarshal(data, &meta); err == nil {
				ghost.Metadata = &meta
				return &meta
			}
		}
	}
	meta := &GhostMetadata{}
	ghost.Metadata = meta
	return meta
}

func humanUserID(loginID networkid.UserLoginID) networkid.UserID {
	return agentremote.HumanUserID("openclaw-user", loginID)
}

var openClawFileFeatures = &event.FileFeatures{
	MimeTypes: map[string]event.CapabilitySupportLevel{
		"*/*": event.CapLevelFullySupported,
	},
	Caption:          event.CapLevelFullySupported,
	MaxCaptionLength: 100000,
	MaxSize:          50 * 1024 * 1024,
}
