package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/shared/jsonutil"
)

type aiPersistedPortalState struct {
	TitleGenerated          bool             `json:"title_generated,omitempty"`
	WelcomeSent             bool             `json:"welcome_sent,omitempty"`
	AutoGreetingSent        bool             `json:"auto_greeting_sent,omitempty"`
	SessionResetAt          int64            `json:"session_reset_at,omitempty"`
	AbortedLastRun          bool             `json:"aborted_last_run,omitempty"`
	CompactionCount         int              `json:"compaction_count,omitempty"`
	SessionBootstrappedAt   int64            `json:"session_bootstrapped_at,omitempty"`
	SessionBootstrapByAgent map[string]int64 `json:"session_bootstrap_by_agent,omitempty"`
	ModuleMeta              map[string]any   `json:"module_meta,omitempty"`
}

type aiPersistedPortalRecord struct {
	State            *aiPersistedPortalState
	ContextEpoch     int64
	NextTurnSequence int64
}

type aiPortalStateStore struct {
	scope *portalScope
}

func newAIPortalStateStore(scope *portalScope) *aiPortalStateStore {
	if scope == nil {
		return nil
	}
	return &aiPortalStateStore{scope: scope}
}

func (store *aiPortalStateStore) Load(ctx context.Context) (*aiPersistedPortalRecord, error) {
	if store == nil || store.scope == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var raw string
	var contextEpoch int64
	var nextTurnSequence int64
	err := store.scope.db.QueryRow(ctx, `
		SELECT state_json, context_epoch, next_turn_sequence
		FROM `+aiPortalStateTable+`
		WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3
	`, store.scope.bridgeID, store.scope.portalID, store.scope.portalReceiver).Scan(&raw, &contextEpoch, &nextTurnSequence)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return &aiPersistedPortalRecord{
			ContextEpoch:     contextEpoch,
			NextTurnSequence: nextTurnSequence,
		}, nil
	}
	var state aiPersistedPortalState
	if err = json.Unmarshal([]byte(raw), &state); err != nil {
		return nil, err
	}
	return &aiPersistedPortalRecord{
		State:            &state,
		ContextEpoch:     contextEpoch,
		NextTurnSequence: nextTurnSequence,
	}, nil
}

func (store *aiPortalStateStore) Ensure(ctx context.Context) (*aiPersistedPortalRecord, error) {
	if store == nil || store.scope == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	nowMs := time.Now().UnixMilli()
	if _, err := store.scope.db.Exec(ctx, `
		INSERT INTO `+aiPortalStateTable+` (
			bridge_id, portal_id, portal_receiver, state_json, context_epoch, next_turn_sequence, updated_at_ms
		) VALUES ($1, $2, $3, '{}', 0, 0, $4)
		ON CONFLICT (bridge_id, portal_id, portal_receiver) DO NOTHING
	`, store.scope.bridgeID, store.scope.portalID, store.scope.portalReceiver, nowMs); err != nil {
		return nil, err
	}
	return store.Load(ctx)
}

func (store *aiPortalStateStore) AllocateTurnSequence(ctx context.Context) (contextEpoch, sequence int64, err error) {
	record, err := store.Ensure(ctx)
	if err != nil {
		return 0, 0, err
	}
	contextEpoch = record.ContextEpoch
	sequence = record.NextTurnSequence + 1
	_, err = store.scope.db.Exec(ctx, `
		UPDATE `+aiPortalStateTable+`
		SET next_turn_sequence=$4, updated_at_ms=$5
		WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3
	`, store.scope.bridgeID, store.scope.portalID, store.scope.portalReceiver, sequence, time.Now().UnixMilli())
	return contextEpoch, sequence, err
}

func (store *aiPortalStateStore) AdvanceContextEpoch(ctx context.Context) error {
	if store == nil || store.scope == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	nowMs := time.Now().UnixMilli()
	_, err := store.scope.db.Exec(ctx, `
		INSERT INTO `+aiPortalStateTable+` (
			bridge_id, portal_id, portal_receiver, state_json, context_epoch, next_turn_sequence, updated_at_ms
		) VALUES ($1, $2, $3, '{}', 1, 0, $4)
		ON CONFLICT (bridge_id, portal_id, portal_receiver) DO UPDATE SET
			context_epoch=`+aiPortalStateTable+`.context_epoch + 1,
			next_turn_sequence=0,
			updated_at_ms=excluded.updated_at_ms
	`, store.scope.bridgeID, store.scope.portalID, store.scope.portalReceiver, nowMs)
	return err
}

func (store *aiPortalStateStore) SaveMetadata(ctx context.Context, meta *PortalMetadata) error {
	if store == nil || store.scope == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	payload, err := json.Marshal(persistedPortalStateFromMeta(meta))
	if err != nil {
		return err
	}
	_, err = store.scope.db.Exec(ctx, `
		INSERT INTO `+aiPortalStateTable+` (
			bridge_id, portal_id, portal_receiver, state_json, context_epoch, next_turn_sequence, updated_at_ms
		) VALUES ($1, $2, $3, $4, 0, 0, $5)
		ON CONFLICT (bridge_id, portal_id, portal_receiver) DO UPDATE SET
			state_json=excluded.state_json,
			updated_at_ms=excluded.updated_at_ms
	`, store.scope.bridgeID, store.scope.portalID, store.scope.portalReceiver, string(payload), time.Now().UnixMilli())
	return err
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

func persistedPortalStateFromMeta(meta *PortalMetadata) *aiPersistedPortalState {
	if meta == nil {
		return &aiPersistedPortalState{}
	}
	return &aiPersistedPortalState{
		TitleGenerated:          meta.TitleGenerated,
		WelcomeSent:             meta.WelcomeSent,
		AutoGreetingSent:        meta.AutoGreetingSent,
		SessionResetAt:          meta.SessionResetAt,
		AbortedLastRun:          meta.AbortedLastRun,
		CompactionCount:         meta.CompactionCount,
		SessionBootstrappedAt:   meta.SessionBootstrappedAt,
		SessionBootstrapByAgent: meta.SessionBootstrapByAgent,
		ModuleMeta:              meta.ModuleMeta,
	}
}

func applyPersistedPortalState(meta *PortalMetadata, state *aiPersistedPortalState) {
	if meta == nil || state == nil {
		return
	}
	meta.TitleGenerated = state.TitleGenerated
	meta.WelcomeSent = state.WelcomeSent
	meta.AutoGreetingSent = state.AutoGreetingSent
	meta.SessionResetAt = state.SessionResetAt
	meta.AbortedLastRun = state.AbortedLastRun
	meta.CompactionCount = state.CompactionCount
	meta.SessionBootstrappedAt = state.SessionBootstrappedAt
	meta.SessionBootstrapByAgent = maps.Clone(state.SessionBootstrapByAgent)
	meta.ModuleMeta = clonePortalStateMap(state.ModuleMeta)
}

func loadAIPortalState(ctx context.Context, portal *bridgev2.Portal) (*aiPersistedPortalState, error) {
	record, err := loadAIPortalRecord(ctx, portal)
	if err != nil || record == nil {
		return nil, err
	}
	return record.State, nil
}

func loadAIPortalRecord(ctx context.Context, portal *bridgev2.Portal) (*aiPersistedPortalRecord, error) {
	return withPortalScopeValue(ctx, portal, func(ctx context.Context, _ *bridgev2.Portal, scope *portalScope) (*aiPersistedPortalRecord, error) {
		return loadAIPortalRecordByScope(ctx, scope)
	})
}

func loadAIPortalRecordByScope(ctx context.Context, scope *portalScope) (*aiPersistedPortalRecord, error) {
	return newAIPortalStateStore(scope).Load(ctx)
}

func advanceAIPortalContextEpoch(ctx context.Context, portal *bridgev2.Portal) error {
	return withPortalScope(ctx, portal, func(ctx context.Context, _ *bridgev2.Portal, scope *portalScope) error {
		return newAIPortalStateStore(scope).AdvanceContextEpoch(ctx)
	})
}

func saveAIPortalState(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) error {
	return withPortalScope(ctx, portal, func(ctx context.Context, portal *bridgev2.Portal, scope *portalScope) error {
		if portal != nil {
			if err := portal.Save(ctx); err != nil {
				return err
			}
		}
		return newAIPortalStateStore(scope).SaveMetadata(ctx, meta)
	})
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
			portal.Bridge.Log.Warn().Err(err).Stringer("portal", portal.PortalKey).Msg("Failed to load AI portal state")
		}
		return
	}
	if state != nil {
		applyPersistedPortalState(meta, state)
	}
}
