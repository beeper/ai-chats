package openclaw

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/pkg/aidb"
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

type openClawLegacyLoginState struct {
	GatewayToken    string `json:"gateway_token,omitempty"`
	GatewayPassword string `json:"gateway_password,omitempty"`
	DeviceToken     string `json:"device_token,omitempty"`
	SessionsSynced  bool   `json:"sessions_synced,omitempty"`
	LastSyncAt      int64  `json:"last_sync_at_ms,omitempty"`
}

var openClawPortalStateBlob = aidb.JSONBlobTable{
	TableName: "openclaw_portal_state",
	KeyColumn: "portal_key",
}

type openClawPortalDBScope struct {
	db        *dbutil.Database
	bridgeID  string
	loginID   string
	portalKey string
}

func openClawPortalDBScopeFor(portal *bridgev2.Portal, login *bridgev2.UserLogin) *openClawPortalDBScope {
	if portal == nil || login == nil || login.Bridge == nil || login.Bridge.DB == nil || login.Bridge.DB.Database == nil {
		return nil
	}
	bridgeID := strings.TrimSpace(string(login.Bridge.DB.BridgeID))
	loginID := strings.TrimSpace(string(login.ID))
	portalKey := strings.TrimSpace(url.PathEscape(string(portal.PortalKey.ID)) + "|" + url.PathEscape(string(portal.PortalKey.Receiver)))
	if bridgeID == "" || loginID == "" || portalKey == "" {
		return nil
	}
	return &openClawPortalDBScope{
		db:        login.Bridge.DB.Database,
		bridgeID:  bridgeID,
		loginID:   loginID,
		portalKey: portalKey,
	}
}

func loadOpenClawPortalState(ctx context.Context, portal *bridgev2.Portal, login *bridgev2.UserLogin) (*openClawPortalState, error) {
	scope := openClawPortalDBScopeFor(portal, login)
	if scope == nil {
		return &openClawPortalState{}, nil
	}
	if err := openClawPortalStateBlob.Ensure(ctx, scope.db); err != nil {
		return nil, err
	}
	state, err := aidb.Load[openClawPortalState](&openClawPortalStateBlob, ctx, scope.db, scope.bridgeID, scope.loginID, scope.portalKey)
	if err != nil {
		return nil, err
	}
	if state == nil {
		if legacy := openClawPortalStateFromMetadata(portal.Metadata); legacy != nil {
			if err := saveOpenClawPortalState(ctx, portal, login, legacy); err != nil {
				return nil, err
			}
			return legacy, nil
		}
		return &openClawPortalState{}, nil
	}
	return state, nil
}

func saveOpenClawPortalState(ctx context.Context, portal *bridgev2.Portal, login *bridgev2.UserLogin, state *openClawPortalState) error {
	scope := openClawPortalDBScopeFor(portal, login)
	if scope == nil || state == nil {
		return nil
	}
	if err := openClawPortalStateBlob.Ensure(ctx, scope.db); err != nil {
		return err
	}
	return aidb.Save(&openClawPortalStateBlob, ctx, scope.db, scope.bridgeID, scope.loginID, scope.portalKey, state)
}

func openClawPortalStateFromMetadata(metadata any) *openClawPortalState {
	if metadata == nil {
		return nil
	}
	if typed, ok := metadata.(*openClawPortalState); ok && typed != nil {
		clone := *typed
		return &clone
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return nil
	}
	var state openClawPortalState
	if err = json.Unmarshal(data, &state); err != nil {
		return nil
	}
	if openClawPortalStateIsEmpty(&state) {
		return nil
	}
	return &state
}

func openClawPortalStateIsEmpty(state *openClawPortalState) bool {
	if state == nil {
		return true
	}
	data, err := json.Marshal(state)
	if err != nil {
		return true
	}
	return string(data) == "{}"
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
	return sdk.EnsureLoginMetadata[UserLoginMetadata](login)
}

type openClawLoginDBScope struct {
	db       *dbutil.Database
	bridgeID string
	loginID  string
}

func openClawLoginDBScopeFor(login *bridgev2.UserLogin) *openClawLoginDBScope {
	if login == nil || login.Bridge == nil || login.Bridge.DB == nil || login.Bridge.DB.Database == nil {
		return nil
	}
	bridgeID := strings.TrimSpace(string(login.Bridge.DB.BridgeID))
	loginID := strings.TrimSpace(string(login.ID))
	if bridgeID == "" || loginID == "" {
		return nil
	}
	return &openClawLoginDBScope{
		db:       login.Bridge.DB.Database,
		bridgeID: bridgeID,
		loginID:  loginID,
	}
}

func ensureOpenClawLoginStateTable(ctx context.Context, login *bridgev2.UserLogin) error {
	scope := openClawLoginDBScopeFor(login)
	if scope == nil {
		return nil
	}
	_, err := scope.db.Exec(ctx, `
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
	scope := openClawLoginDBScopeFor(login)
	if scope == nil {
		return &openClawPersistedLoginState{}, nil
	}
	if err := ensureOpenClawLoginStateTable(ctx, login); err != nil {
		return nil, err
	}
	state := &openClawPersistedLoginState{}
	err := scope.db.QueryRow(ctx, `
		SELECT gateway_token, gateway_password, device_token, sessions_synced, last_sync_at_ms
		FROM openclaw_login_state
		WHERE bridge_id=$1 AND login_id=$2
	`, scope.bridgeID, scope.loginID).Scan(
		&state.GatewayToken,
		&state.GatewayPassword,
		&state.DeviceToken,
		&state.SessionsSynced,
		&state.LastSyncAt,
	)
	if err == sql.ErrNoRows {
		if legacy := openClawLoginStateFromMetadata(login); legacy != nil {
			if saveErr := saveOpenClawLoginState(ctx, login, legacy); saveErr != nil {
				return nil, saveErr
			}
			return legacy, nil
		}
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	return state, nil
}

func saveOpenClawLoginState(ctx context.Context, login *bridgev2.UserLogin, state *openClawPersistedLoginState) error {
	scope := openClawLoginDBScopeFor(login)
	if scope == nil || state == nil {
		return nil
	}
	if err := ensureOpenClawLoginStateTable(ctx, login); err != nil {
		return err
	}
	_, err := scope.db.Exec(ctx, `
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
		scope.bridgeID,
		scope.loginID,
		state.GatewayToken,
		state.GatewayPassword,
		state.DeviceToken,
		state.SessionsSynced,
		state.LastSyncAt,
		time.Now().UnixMilli(),
	)
	return err
}

func openClawLoginStateFromMetadata(login *bridgev2.UserLogin) *openClawPersistedLoginState {
	if login == nil || login.Metadata == nil {
		return nil
	}
	var legacy openClawLegacyLoginState
	data, err := json.Marshal(login.Metadata)
	if err != nil {
		return nil
	}
	if err = json.Unmarshal(data, &legacy); err != nil {
		return nil
	}
	if legacy.GatewayToken == "" && legacy.GatewayPassword == "" && legacy.DeviceToken == "" && !legacy.SessionsSynced && legacy.LastSyncAt == 0 {
		return nil
	}
	return &openClawPersistedLoginState{
		GatewayToken:    legacy.GatewayToken,
		GatewayPassword: legacy.GatewayPassword,
		DeviceToken:     legacy.DeviceToken,
		SessionsSynced:  legacy.SessionsSynced,
		LastSyncAt:      legacy.LastSyncAt,
	}
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

var openClawFileFeatures = &event.FileFeatures{
	MimeTypes: map[string]event.CapabilitySupportLevel{
		"*/*": event.CapLevelFullySupported,
	},
	Caption:          event.CapLevelFullySupported,
	MaxCaptionLength: 100000,
	MaxSize:          50 * 1024 * 1024,
}
