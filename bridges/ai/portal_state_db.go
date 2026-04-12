package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"strings"
	"time"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/shared/jsonutil"
)

type aiPersistedPortalState struct {
	AckReactionEmoji       string            `json:"ack_reaction_emoji,omitempty"`
	AckReactionRemoveAfter bool              `json:"ack_reaction_remove_after,omitempty"`
	PDFConfig              *PDFConfig        `json:"pdf_config,omitempty"`
	Slug                   string            `json:"slug,omitempty"`
	Title                  string            `json:"title,omitempty"`
	TitleGenerated         bool              `json:"title_generated,omitempty"`
	WelcomeSent            bool              `json:"welcome_sent,omitempty"`
	AutoGreetingSent       bool              `json:"auto_greeting_sent,omitempty"`
	SessionResetAt         int64             `json:"session_reset_at,omitempty"`
	AbortedLastRun         bool              `json:"aborted_last_run,omitempty"`
	CompactionCount        int               `json:"compaction_count,omitempty"`
	SessionBootstrappedAt  int64             `json:"session_bootstrapped_at,omitempty"`
	SessionBootstrapByAgent map[string]int64  `json:"session_bootstrap_by_agent,omitempty"`
	ModuleMeta             map[string]any    `json:"module_meta,omitempty"`
	SubagentParentRoomID   string            `json:"subagent_parent_room_id,omitempty"`
	DebounceMs             int               `json:"debounce_ms,omitempty"`
	TypingMode             string            `json:"typing_mode,omitempty"`
	TypingIntervalSeconds   *int              `json:"typing_interval_seconds,omitempty"`
}

type portalStateScope struct {
	db       *dbutil.Database
	bridgeID string
	loginID  string
	portalID string
}

func portalStateScopeForPortal(portal *bridgev2.Portal) *portalStateScope {
	db := bridgeDBFromPortal(portal)
	if db == nil || portal == nil || portal.Bridge == nil || portal.Bridge.DB == nil {
		return nil
	}
	bridgeID := string(portal.Bridge.DB.BridgeID)
	loginID := strings.TrimSpace(string(portal.Receiver))
	portalID := strings.TrimSpace(string(portal.PortalKey.ID))
	if bridgeID == "" || loginID == "" || portalID == "" {
		return nil
	}
	return &portalStateScope{
		db:       db,
		bridgeID: bridgeID,
		loginID:  loginID,
		portalID: portalID,
	}
}

func clonePortalStateMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = jsonutil.DeepCloneAny(v)
	}
	return out
}

func clonePortalState(src *aiPersistedPortalState) *aiPersistedPortalState {
	if src == nil {
		return &aiPersistedPortalState{}
	}
	clone := *src
	if src.PDFConfig != nil {
		pdf := *src.PDFConfig
		clone.PDFConfig = &pdf
	}
	if src.TypingIntervalSeconds != nil {
		interval := *src.TypingIntervalSeconds
		clone.TypingIntervalSeconds = &interval
	}
	if src.SessionBootstrapByAgent != nil {
		clone.SessionBootstrapByAgent = maps.Clone(src.SessionBootstrapByAgent)
	}
	if src.ModuleMeta != nil {
		clone.ModuleMeta = clonePortalStateMap(src.ModuleMeta)
	}
	return &clone
}

func persistedPortalStateFromMeta(meta *PortalMetadata) *aiPersistedPortalState {
	if meta == nil {
		return &aiPersistedPortalState{}
	}
	return &aiPersistedPortalState{
		AckReactionEmoji:       meta.AckReactionEmoji,
		AckReactionRemoveAfter: meta.AckReactionRemoveAfter,
		PDFConfig:              meta.PDFConfig,
		Slug:                   meta.Slug,
		Title:                  meta.Title,
		TitleGenerated:         meta.TitleGenerated,
		WelcomeSent:            meta.WelcomeSent,
		AutoGreetingSent:       meta.AutoGreetingSent,
		SessionResetAt:         meta.SessionResetAt,
		AbortedLastRun:         meta.AbortedLastRun,
		CompactionCount:        meta.CompactionCount,
		SessionBootstrappedAt:  meta.SessionBootstrappedAt,
		SessionBootstrapByAgent: meta.SessionBootstrapByAgent,
		ModuleMeta:             meta.ModuleMeta,
		SubagentParentRoomID:   meta.SubagentParentRoomID,
		DebounceMs:             meta.DebounceMs,
		TypingMode:             meta.TypingMode,
		TypingIntervalSeconds:   meta.TypingIntervalSeconds,
	}
}

func applyPersistedPortalState(meta *PortalMetadata, state *aiPersistedPortalState) {
	if meta == nil || state == nil {
		return
	}
	meta.AckReactionEmoji = state.AckReactionEmoji
	meta.AckReactionRemoveAfter = state.AckReactionRemoveAfter
	meta.PDFConfig = state.PDFConfig
	meta.Slug = state.Slug
	meta.Title = state.Title
	meta.TitleGenerated = state.TitleGenerated
	meta.WelcomeSent = state.WelcomeSent
	meta.AutoGreetingSent = state.AutoGreetingSent
	meta.SessionResetAt = state.SessionResetAt
	meta.AbortedLastRun = state.AbortedLastRun
	meta.CompactionCount = state.CompactionCount
	meta.SessionBootstrappedAt = state.SessionBootstrappedAt
	meta.SessionBootstrapByAgent = maps.Clone(state.SessionBootstrapByAgent)
	meta.ModuleMeta = clonePortalStateMap(state.ModuleMeta)
	meta.SubagentParentRoomID = state.SubagentParentRoomID
	meta.DebounceMs = state.DebounceMs
	meta.TypingMode = state.TypingMode
	if state.TypingIntervalSeconds != nil {
		interval := *state.TypingIntervalSeconds
		meta.TypingIntervalSeconds = &interval
	} else {
		meta.TypingIntervalSeconds = nil
	}
}

func ensurePortalStateTable(ctx context.Context, portal *bridgev2.Portal) error {
	scope := portalStateScopeForPortal(portal)
	if scope == nil {
		return nil
	}
	_, err := scope.db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS `+aiPortalStateTable+` (
			bridge_id TEXT NOT NULL,
			login_id TEXT NOT NULL,
			portal_id TEXT NOT NULL,
			state_json TEXT NOT NULL DEFAULT '',
			updated_at_ms INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (bridge_id, login_id, portal_id)
		)
	`)
	return err
}

func loadAIPortalState(ctx context.Context, portal *bridgev2.Portal) (*aiPersistedPortalState, error) {
	scope := portalStateScopeForPortal(portal)
	if scope == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ensurePortalStateTable(ctx, portal); err != nil {
		return nil, err
	}
	var raw string
	err := scope.db.QueryRow(ctx, `
		SELECT state_json
		FROM `+aiPortalStateTable+`
		WHERE bridge_id=$1 AND login_id=$2 AND portal_id=$3
	`, scope.bridgeID, scope.loginID, scope.portalID).Scan(&raw)
	if err == sql.ErrNoRows || strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state aiPersistedPortalState
	if err = json.Unmarshal([]byte(raw), &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func saveAIPortalState(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) error {
	scope := portalStateScopeForPortal(portal)
	if scope == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ensurePortalStateTable(ctx, portal); err != nil {
		return err
	}
	payload, err := json.Marshal(persistedPortalStateFromMeta(meta))
	if err != nil {
		return err
	}
	_, err = scope.db.Exec(ctx, `
		INSERT INTO `+aiPortalStateTable+` (
			bridge_id, login_id, portal_id, state_json, updated_at_ms
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (bridge_id, login_id, portal_id) DO UPDATE SET
			state_json=excluded.state_json,
			updated_at_ms=excluded.updated_at_ms
	`, scope.bridgeID, scope.loginID, scope.portalID, string(payload), time.Now().UnixMilli())
	return err
}

func loadPortalStateIntoMetadata(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) {
	if meta == nil || meta.portalStateLoaded {
		return
	}
	meta.portalStateLoaded = true
	state, err := loadAIPortalState(ctx, portal)
	if err != nil {
		meta.portalStateLoaded = false
		if portal != nil && portal.Bridge != nil {
			portal.Bridge.Log.Warn().Err(err).Str("portal", portal.PortalKey.String()).Msg("Failed to load AI portal state")
		}
		return
	}
	if state != nil {
		applyPersistedPortalState(meta, state)
	}
}
