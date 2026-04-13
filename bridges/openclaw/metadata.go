package openclaw

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/pkg/aidb"
	"github.com/beeper/agentremote/pkg/shared/openclawconv"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/sdk"
)

type UserLoginMetadata struct {
	Provider     string `json:"provider,omitempty"`
	GatewayURL   string `json:"gateway_url,omitempty"`
	GatewayLabel string `json:"gateway_label,omitempty"`
}

type PortalMetadata struct {
	IsOpenClawRoom bool `json:"is_openclaw_room,omitempty"`
}

type openClawPortalState struct {
	OpenClawGatewayID             string         `json:"openclaw_gateway_id,omitempty"`
	OpenClawSessionID             string         `json:"openclaw_session_id,omitempty"`
	OpenClawSessionKey            string         `json:"openclaw_session_key,omitempty"`
	OpenClawSpawnedBy             string         `json:"openclaw_spawned_by,omitempty"`
	OpenClawDMTargetAgentID       string         `json:"openclaw_dm_target_agent_id,omitempty"`
	OpenClawDMTargetAgentName     string         `json:"openclaw_dm_target_agent_name,omitempty"`
	OpenClawDMCreatedFromContact  bool           `json:"openclaw_dm_created_from_contact,omitempty"`
	OpenClawSessionKind           string         `json:"openclaw_session_kind,omitempty"`
	OpenClawSessionLabel          string         `json:"openclaw_session_label,omitempty"`
	OpenClawDisplayName           string         `json:"openclaw_display_name,omitempty"`
	OpenClawDerivedTitle          string         `json:"openclaw_derived_title,omitempty"`
	OpenClawLastMessagePreview    string         `json:"openclaw_last_message_preview,omitempty"`
	OpenClawChannel               string         `json:"openclaw_channel,omitempty"`
	OpenClawSubject               string         `json:"openclaw_subject,omitempty"`
	OpenClawGroupChannel          string         `json:"openclaw_group_channel,omitempty"`
	OpenClawSpace                 string         `json:"openclaw_space,omitempty"`
	OpenClawChatType              string         `json:"openclaw_chat_type,omitempty"`
	OpenClawOrigin                string         `json:"openclaw_origin,omitempty"`
	OpenClawAgentID               string         `json:"openclaw_agent_id,omitempty"`
	OpenClawSystemSent            bool           `json:"openclaw_system_sent,omitempty"`
	OpenClawAbortedLastRun        bool           `json:"openclaw_aborted_last_run,omitempty"`
	ThinkingLevel                 string         `json:"thinking_level,omitempty"`
	FastMode                      bool           `json:"fast_mode,omitempty"`
	VerboseLevel                  string         `json:"verbose_level,omitempty"`
	ReasoningLevel                string         `json:"reasoning_level,omitempty"`
	ElevatedLevel                 string         `json:"elevated_level,omitempty"`
	SendPolicy                    string         `json:"send_policy,omitempty"`
	InputTokens                   int64          `json:"input_tokens,omitempty"`
	OutputTokens                  int64          `json:"output_tokens,omitempty"`
	TotalTokens                   int64          `json:"total_tokens,omitempty"`
	TotalTokensFresh              bool           `json:"total_tokens_fresh,omitempty"`
	EstimatedCostUSD              float64        `json:"estimated_cost_usd,omitempty"`
	Status                        string         `json:"status,omitempty"`
	StartedAt                     int64          `json:"started_at,omitempty"`
	EndedAt                       int64          `json:"ended_at,omitempty"`
	RuntimeMs                     int64          `json:"runtime_ms,omitempty"`
	ParentSessionKey              string         `json:"parent_session_key,omitempty"`
	ChildSessions                 []string       `json:"child_sessions,omitempty"`
	ResponseUsage                 string         `json:"response_usage,omitempty"`
	ModelProvider                 string         `json:"model_provider,omitempty"`
	Model                         string         `json:"model,omitempty"`
	ContextTokens                 int64          `json:"context_tokens,omitempty"`
	DeliveryContext               map[string]any `json:"delivery_context,omitempty"`
	LastChannel                   string         `json:"last_channel,omitempty"`
	LastTo                        string         `json:"last_to,omitempty"`
	LastAccountID                 string         `json:"last_account_id,omitempty"`
	SessionUpdatedAt              int64          `json:"session_updated_at,omitempty"`
	OpenClawPreviewSnippet        string         `json:"openclaw_preview_snippet,omitempty"`
	OpenClawDefaultAgentID        string         `json:"openclaw_default_agent_id,omitempty"`
	OpenClawToolProfile           string         `json:"openclaw_tool_profile,omitempty"`
	OpenClawToolCount             int            `json:"openclaw_tool_count,omitempty"`
	OpenClawKnownModelCount       int            `json:"openclaw_known_model_count,omitempty"`
	OpenClawLastPreviewAt         int64          `json:"openclaw_last_preview_at,omitempty"`
	HistoryMode                   string         `json:"history_mode,omitempty"`
	RecentHistoryLimit            int            `json:"recent_history_limit,omitempty"`
	LastHistorySyncAt             int64          `json:"last_history_sync_at,omitempty"`
	LastTranscriptFingerprint     string         `json:"last_transcript_fingerprint,omitempty"`
	LastLiveSeq                   int64          `json:"last_live_seq,omitempty"`
	BackgroundBackfillStartedAt   int64          `json:"background_backfill_started_at,omitempty"`
	BackgroundBackfillCompletedAt int64          `json:"background_backfill_completed_at,omitempty"`
	BackgroundBackfillCursor      string         `json:"background_backfill_cursor,omitempty"`
	BackgroundBackfillStatus      string         `json:"background_backfill_status,omitempty"`
	BackgroundBackfillError       string         `json:"background_backfill_error,omitempty"`
}

type openClawPersistedLoginState struct {
	GatewayToken    string
	GatewayPassword string
	DeviceToken     string
	SessionsSynced  bool
	LastSyncAt      int64
}

var openClawPortalStateBlob = aidb.JSONBlobTable{
	TableName: "openclaw_portal_state",
	KeyColumn: "portal_key",
}

func openClawPortalBlobScope(portal *bridgev2.Portal, login *bridgev2.UserLogin) *aidb.BlobScope {
	if portal == nil || login == nil || login.Bridge == nil || login.Bridge.DB == nil || login.Bridge.DB.Database == nil {
		return nil
	}
	bridgeID := strings.TrimSpace(string(login.Bridge.DB.BridgeID))
	loginID := strings.TrimSpace(string(login.ID))
	portalKey := strings.TrimSpace(url.PathEscape(string(portal.PortalKey.ID)) + "|" + url.PathEscape(string(portal.PortalKey.Receiver)))
	if bridgeID == "" || loginID == "" || portalKey == "" {
		return nil
	}
	return &aidb.BlobScope{
		Table:    &openClawPortalStateBlob,
		DB:       login.Bridge.DB.Database,
		BridgeID: bridgeID,
		LoginID:  loginID,
		Key:      portalKey,
	}
}

func loadOpenClawPortalState(ctx context.Context, portal *bridgev2.Portal, login *bridgev2.UserLogin) (*openClawPortalState, error) {
	return aidb.LoadScopedOrNew[openClawPortalState](ctx, openClawPortalBlobScope(portal, login))
}

func saveOpenClawPortalState(ctx context.Context, portal *bridgev2.Portal, login *bridgev2.UserLogin, state *openClawPortalState) error {
	return aidb.SaveScoped(ctx, openClawPortalBlobScope(portal, login), state)
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
	sdk.BaseMessageMetadata
	SessionID      string           `json:"session_id,omitempty"`
	SessionKey     string           `json:"session_key,omitempty"`
	RunID          string           `json:"run_id,omitempty"`
	ErrorText      string           `json:"error_text,omitempty"`
	TotalTokens    int64            `json:"total_tokens,omitempty"`
	Attachments    []map[string]any `json:"attachments,omitempty"`
	FirstTokenAtMs int64            `json:"first_token_at_ms,omitempty"`
}

type openClawMessageMetadataParams struct {
	Base           sdk.BaseMessageMetadata
	SessionID      string
	SessionKey     string
	RunID          string
	ErrorText      string
	TotalTokens    int64
	Attachments    []map[string]any
	FirstTokenAtMs int64
}

func (mm *MessageMetadata) CopyFrom(other any) {
	src, ok := other.(*MessageMetadata)
	if !ok || src == nil {
		return
	}
	mm.BaseMessageMetadata.CopyFromBase(&src.BaseMessageMetadata)
	sdk.CopyNonZero(&mm.SessionID, src.SessionID)
	sdk.CopyNonZero(&mm.SessionKey, src.SessionKey)
	sdk.CopyNonZero(&mm.RunID, src.RunID)
	sdk.CopyNonZero(&mm.ErrorText, src.ErrorText)
	sdk.CopyNonZero(&mm.TotalTokens, src.TotalTokens)
	sdk.CopyMapSlice(&mm.Attachments, src.Attachments)
	sdk.CopyNonZero(&mm.FirstTokenAtMs, src.FirstTokenAtMs)
}

func openClawMetadataExtras(sessionID, sessionKey, errorText string) map[string]any {
	extras := map[string]any{}
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
		extras["session_id"] = sessionID
	}
	if sessionKey = strings.TrimSpace(sessionKey); sessionKey != "" {
		extras["session_key"] = sessionKey
	}
	if errorText = strings.TrimSpace(errorText); errorText != "" {
		extras["error_text"] = errorText
	}
	if len(extras) == 0 {
		return nil
	}
	return extras
}

func buildOpenClawUIMessageMetadata(params sdk.UIMessageMetadataParams, sessionID, sessionKey, errorText string) map[string]any {
	params.Extras = openClawMetadataExtras(sessionID, sessionKey, errorText)
	return sdk.BuildUIMessageMetadata(params)
}

func buildOpenClawMessageMetadata(params openClawMessageMetadataParams) *MessageMetadata {
	metadata := &MessageMetadata{
		BaseMessageMetadata: params.Base,
		SessionID:           strings.TrimSpace(params.SessionID),
		SessionKey:          strings.TrimSpace(params.SessionKey),
		RunID:               strings.TrimSpace(params.RunID),
		ErrorText:           strings.TrimSpace(params.ErrorText),
		TotalTokens:         params.TotalTokens,
		Attachments:         params.Attachments,
		FirstTokenAtMs:      params.FirstTokenAtMs,
	}
	return metadata
}

func loginMetadata(login *bridgev2.UserLogin) *UserLoginMetadata {
	return sdk.EnsureLoginMetadata[UserLoginMetadata](login)
}

func openClawLoginBlobScope(login *bridgev2.UserLogin) *aidb.BlobScope {
	if login == nil || login.Bridge == nil || login.Bridge.DB == nil || login.Bridge.DB.Database == nil {
		return nil
	}
	bridgeID := strings.TrimSpace(string(login.Bridge.DB.BridgeID))
	loginID := strings.TrimSpace(string(login.ID))
	if bridgeID == "" || loginID == "" {
		return nil
	}
	return &aidb.BlobScope{
		DB:       login.Bridge.DB.Database,
		BridgeID: bridgeID,
		LoginID:  loginID,
	}
}

func ensureOpenClawLoginStateTable(ctx context.Context, login *bridgev2.UserLogin) error {
	scope := openClawLoginBlobScope(login)
	if scope == nil {
		return nil
	}
	_, err := scope.DB.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS openclaw_login_state (
			bridge_id TEXT NOT NULL,
			login_id TEXT NOT NULL,
			gateway_token TEXT NOT NULL DEFAULT '',
			gateway_password TEXT NOT NULL DEFAULT '',
			device_token TEXT NOT NULL DEFAULT '',
			sessions_synced INTEGER NOT NULL DEFAULT 0,
			last_sync_at_ms INTEGER NOT NULL DEFAULT 0,
			updated_at_ms INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (bridge_id, login_id)
		)
	`)
	return err
}

func loadOpenClawLoginState(ctx context.Context, login *bridgev2.UserLogin) (*openClawPersistedLoginState, error) {
	scope := openClawLoginBlobScope(login)
	if scope == nil {
		return &openClawPersistedLoginState{}, nil
	}
	if err := ensureOpenClawLoginStateTable(ctx, login); err != nil {
		return nil, err
	}
	state := &openClawPersistedLoginState{}
	err := scope.DB.QueryRow(ctx, `
		SELECT gateway_token, gateway_password, device_token, sessions_synced, last_sync_at_ms
		FROM openclaw_login_state
		WHERE bridge_id=$1 AND login_id=$2
	`, scope.BridgeID, scope.LoginID).Scan(
		&state.GatewayToken,
		&state.GatewayPassword,
		&state.DeviceToken,
		&state.SessionsSynced,
		&state.LastSyncAt,
	)
	if err == sql.ErrNoRows {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	return state, nil
}

func saveOpenClawLoginState(ctx context.Context, login *bridgev2.UserLogin, state *openClawPersistedLoginState) error {
	scope := openClawLoginBlobScope(login)
	if scope == nil || state == nil {
		return nil
	}
	if err := ensureOpenClawLoginStateTable(ctx, login); err != nil {
		return err
	}
	_, err := scope.DB.Exec(ctx, `
		INSERT INTO openclaw_login_state (
			bridge_id, login_id, gateway_token, gateway_password, device_token, sessions_synced, last_sync_at_ms, updated_at_ms
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (bridge_id, login_id) DO UPDATE SET
			gateway_token=excluded.gateway_token,
			gateway_password=excluded.gateway_password,
			device_token=excluded.device_token,
			sessions_synced=excluded.sessions_synced,
			last_sync_at_ms=excluded.last_sync_at_ms,
			updated_at_ms=excluded.updated_at_ms
	`,
		scope.BridgeID,
		scope.LoginID,
		state.GatewayToken,
		state.GatewayPassword,
		state.DeviceToken,
		state.SessionsSynced,
		state.LastSyncAt,
		time.Now().UnixMilli(),
	)
	return err
}

func portalMeta(portal *bridgev2.Portal) *PortalMetadata {
	return sdk.EnsurePortalMetadata[PortalMetadata](portal)
}

func ghostMeta(ghost *bridgev2.Ghost) *GhostMetadata {
	if ghost == nil {
		return &GhostMetadata{}
	}
	if typed, ok := ghost.Metadata.(*GhostMetadata); ok && typed != nil {
		return typed
	}
	// Handle untyped metadata (map[string]any, map[string]string, etc.)
	// by round-tripping through JSON.
	if ghost.Metadata != nil {
		if data, err := json.Marshal(ghost.Metadata); err == nil {
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
	return sdk.HumanUserID("openclaw-user", loginID)
}

// applyGhostMetadataUpdates applies non-empty fields from desired onto current,
// returning true if any field changed.
func applyGhostMetadataUpdates(current, desired *GhostMetadata) bool {
	changed := false
	changed = setIfChanged(&current.OpenClawAgentID, desired.OpenClawAgentID) || changed
	changed = setIfChanged(&current.OpenClawAgentName, desired.OpenClawAgentName) || changed
	changed = setIfChanged(&current.OpenClawAgentAvatarURL, desired.OpenClawAgentAvatarURL) || changed
	changed = setIfChanged(&current.OpenClawAgentEmoji, desired.OpenClawAgentEmoji) || changed
	changed = setIfChanged(&current.OpenClawAgentRole, desired.OpenClawAgentRole) || changed
	if current.LastSeenAt != desired.LastSeenAt {
		current.LastSeenAt = desired.LastSeenAt
		changed = true
	}
	return changed
}

// setIfChanged updates dst to value (trimmed) when value is non-empty and
// differs from the current dst. Returns true when a change was made.
func setIfChanged(dst *string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || *dst == value {
		return false
	}
	*dst = value
	return true
}

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
	if strings.TrimSpace(string(loginID)) == "" {
		return openClawGhostUserID(agentID)
	}
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

var openClawFileFeatures = &event.FileFeatures{
	MimeTypes: map[string]event.CapabilitySupportLevel{
		"*/*": event.CapLevelFullySupported,
	},
	Caption:          event.CapLevelFullySupported,
	MaxCaptionLength: 100000,
	MaxSize:          50 * 1024 * 1024,
}
