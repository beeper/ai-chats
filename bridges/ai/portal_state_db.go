package ai

import (
	"context"
	"database/sql"
	"time"

	"maunium.net/go/mautrix/bridgev2"
)

type aiPersistedPortalRecord struct {
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
	var contextEpoch int64
	var nextTurnSequence int64
	err := store.scope.db.QueryRow(ctx, `
		SELECT context_epoch, next_turn_sequence
		FROM `+aiPortalStateTable+`
		WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3
	`, store.scope.bridgeID, store.scope.portalID, store.scope.portalReceiver).Scan(&contextEpoch, &nextTurnSequence)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &aiPersistedPortalRecord{
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
			bridge_id, portal_id, portal_receiver, context_epoch, next_turn_sequence, updated_at_ms
		) VALUES ($1, $2, $3, 0, 0, $4)
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
			bridge_id, portal_id, portal_receiver, context_epoch, next_turn_sequence, updated_at_ms
		) VALUES ($1, $2, $3, 1, 0, $4)
		ON CONFLICT (bridge_id, portal_id, portal_receiver) DO UPDATE SET
			context_epoch=`+aiPortalStateTable+`.context_epoch + 1,
			next_turn_sequence=0,
			updated_at_ms=excluded.updated_at_ms
	`, store.scope.bridgeID, store.scope.portalID, store.scope.portalReceiver, nowMs)
	return err
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
