package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/sdk"
)

const (
	aiTurnKindConversation = "conversation"
	aiTurnKindInternal     = "internal"

	aiTurnRefKindMessageID = "message_id"
	aiTurnRefKindEventID   = "event_id"
)

type aiPersistedPortalRecord struct {
	ContextEpoch     int64
	NextTurnSequence int64
}

type aiTurnRecord struct {
	TurnID           string
	Sequence         int64
	ContextEpoch     int64
	Kind             string
	Source           string
	Role             string
	SenderID         networkid.UserID
	IncludeInHistory bool
	TurnData         sdk.TurnData
	Metadata         *MessageMetadata
	MessageID        networkid.MessageID
	EventID          id.EventID
	CreatedAtMs      int64
	UpdatedAtMs      int64
}

type aiTurnUpsert struct {
	TurnID           string
	Kind             string
	Source           string
	MessageID        networkid.MessageID
	EventID          id.EventID
	SenderID         networkid.UserID
	IncludeInHistory bool
	Timestamp        time.Time
	TurnData         sdk.TurnData
	Metadata         *MessageMetadata
}

func normalizeAITurnMetadata(meta *MessageMetadata, turnData sdk.TurnData) *MessageMetadata {
	clean := cloneMessageMetadata(meta)
	if clean == nil {
		clean = &MessageMetadata{}
	}
	clean.CanonicalTurnData = turnData.ToMap()
	if clean.Role == "" {
		clean.Role = strings.TrimSpace(turnData.Role)
	}
	if clean.Body == "" {
		clean.Body = sdk.TurnText(turnData)
	}
	return clean
}

func encodeAITurnMetadata(meta *MessageMetadata) (string, error) {
	clean := cloneMessageMetadata(meta)
	if clean == nil {
		clean = &MessageMetadata{}
	}
	clean.CanonicalTurnData = nil
	raw, err := json.Marshal(clean)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func decodeAITurnMetadata(raw string, turnData sdk.TurnData) (*MessageMetadata, error) {
	if strings.TrimSpace(raw) == "" {
		return normalizeAITurnMetadata(nil, turnData), nil
	}
	var meta MessageMetadata
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return nil, err
	}
	return normalizeAITurnMetadata(&meta, turnData), nil
}

func loadAIPortalRecordByScope(ctx context.Context, scope *portalScope) (*aiPersistedPortalRecord, error) {
	if scope == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var record aiPersistedPortalRecord
	err := scope.db.QueryRow(ctx, `
		SELECT context_epoch, next_turn_sequence
		FROM `+aiPortalStateTable+`
		WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3
	`, scope.bridgeID, scope.portalID, scope.portalReceiver).Scan(
		&record.ContextEpoch,
		&record.NextTurnSequence,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func ensureAIPortalRecordByScope(ctx context.Context, scope *portalScope) (*aiPersistedPortalRecord, error) {
	if scope == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := scope.db.Exec(ctx, `
		INSERT INTO `+aiPortalStateTable+` (
			bridge_id, portal_id, portal_receiver, context_epoch, next_turn_sequence
		) VALUES ($1, $2, $3, 0, 0)
		ON CONFLICT (bridge_id, portal_id, portal_receiver) DO NOTHING
	`, scope.bridgeID, scope.portalID, scope.portalReceiver); err != nil {
		return nil, err
	}
	return loadAIPortalRecordByScope(ctx, scope)
}

func allocateAITurnSequence(ctx context.Context, scope *portalScope) (contextEpoch, sequence int64, err error) {
	if scope == nil {
		return 0, 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	err = scope.db.QueryRow(ctx, `
		INSERT INTO `+aiPortalStateTable+` (
			bridge_id, portal_id, portal_receiver, context_epoch, next_turn_sequence
		) VALUES ($1, $2, $3, 0, 1)
		ON CONFLICT (bridge_id, portal_id, portal_receiver) DO UPDATE SET
			next_turn_sequence=`+aiPortalStateTable+`.next_turn_sequence + 1
		RETURNING context_epoch, next_turn_sequence
	`, scope.bridgeID, scope.portalID, scope.portalReceiver).Scan(&contextEpoch, &sequence)
	return contextEpoch, sequence, err
}

func loadAITurnByRefByScope(
	ctx context.Context,
	scope *portalScope,
	messageID networkid.MessageID,
	eventID id.EventID,
) (*aiTurnRecord, error) {
	if scope == nil {
		return nil, nil
	}
	if row, err := loadAITurnByRefValue(ctx, scope, aiTurnRefKindMessageID, strings.TrimSpace(string(messageID))); err != nil || row != nil {
		return row, err
	}
	return loadAITurnByRefValue(ctx, scope, aiTurnRefKindEventID, strings.TrimSpace(eventID.String()))
}

func loadAITurnByRefValue(ctx context.Context, scope *portalScope, refKind, refValue string) (*aiTurnRecord, error) {
	if scope == nil || refKind == "" || strings.TrimSpace(refValue) == "" {
		return nil, nil
	}
	rows, err := queryAITurnRows(ctx, scope, aiTurnQuery{
		refKind:  refKind,
		refValue: refValue,
		limit:    1,
	})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

func upsertAITurnByScope(
	ctx context.Context,
	scope *portalScope,
	portal *bridgev2.Portal,
	entry aiTurnUpsert,
) error {
	if scope == nil {
		return fmt.Errorf("ai turn scope unavailable for portal %s", portal.PortalKey)
	}
	role := strings.TrimSpace(entry.TurnData.Role)
	if role == "" && entry.Metadata != nil {
		role = strings.TrimSpace(entry.Metadata.Role)
	}
	if role == "" {
		return nil
	}
	entry.TurnData.Role = role
	if strings.TrimSpace(entry.TurnID) != "" {
		entry.TurnData.ID = strings.TrimSpace(entry.TurnID)
	}
	return scope.db.DoTxn(ctx, nil, func(ctx context.Context) error {
		existing, err := resolveExistingAITurnForUpdate(ctx, scope, entry)
		if err != nil {
			return err
		}

		turnID := strings.TrimSpace(entry.TurnData.ID)
		contextEpoch := int64(0)
		sequence := int64(0)
		createdAtMs := entry.Timestamp.UnixMilli()
		if entry.Timestamp.IsZero() {
			createdAtMs = time.Now().UnixMilli()
		}
		if existing != nil {
			turnID = existing.TurnID
			contextEpoch = existing.ContextEpoch
			sequence = existing.Sequence
			if existing.CreatedAtMs > 0 {
				createdAtMs = existing.CreatedAtMs
			}
		} else {
			if turnID == "" {
				turnID = sdk.NewTurnID()
			}
			contextEpoch, sequence, err = allocateAITurnSequence(ctx, scope)
			if err != nil {
				return err
			}
		}
		entry.TurnData.ID = turnID
		meta := normalizeAITurnMetadata(entry.Metadata, entry.TurnData)
		metaJSON, err := encodeAITurnMetadata(meta)
		if err != nil {
			return err
		}
		turnJSON, err := json.Marshal(entry.TurnData.ToMap())
		if err != nil {
			return err
		}
		nowMs := time.Now().UnixMilli()
		if _, err = scope.db.Exec(ctx, `
			INSERT INTO `+aiTurnsTable+` (
				bridge_id, portal_id, portal_receiver, turn_id, context_epoch, sequence, kind, source, role,
				sender_id, include_in_history, turn_data_json, meta_json, created_at_ms, updated_at_ms
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
			ON CONFLICT (bridge_id, portal_id, portal_receiver, turn_id) DO UPDATE SET
				kind=excluded.kind,
				source=excluded.source,
				role=excluded.role,
				sender_id=excluded.sender_id,
				include_in_history=excluded.include_in_history,
				turn_data_json=excluded.turn_data_json,
				meta_json=excluded.meta_json,
				updated_at_ms=excluded.updated_at_ms
		`, scope.bridgeID, scope.portalID, scope.portalReceiver, turnID, contextEpoch, sequence,
			normalizeAITurnKind(entry.Kind), strings.TrimSpace(entry.Source), role, string(entry.SenderID),
			entry.IncludeInHistory, string(turnJSON), metaJSON, createdAtMs, nowMs); err != nil {
			return err
		}
		if err := replaceAITurnRef(ctx, scope, turnID, aiTurnRefKindMessageID, strings.TrimSpace(string(entry.MessageID))); err != nil {
			return err
		}
		if err := replaceAITurnRef(ctx, scope, turnID, aiTurnRefKindEventID, strings.TrimSpace(entry.EventID.String())); err != nil {
			return err
		}
		return nil
	})
}

func normalizeAITurnKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case aiTurnKindInternal:
		return aiTurnKindInternal
	default:
		return aiTurnKindConversation
	}
}

func resolveExistingAITurnForUpdate(ctx context.Context, scope *portalScope, entry aiTurnUpsert) (*aiTurnRecord, error) {
	if row, err := loadAITurnByRefValue(ctx, scope, aiTurnRefKindMessageID, strings.TrimSpace(string(entry.MessageID))); err != nil || row != nil {
		return row, err
	}
	if row, err := loadAITurnByRefValue(ctx, scope, aiTurnRefKindEventID, strings.TrimSpace(entry.EventID.String())); err != nil || row != nil {
		return row, err
	}
	if strings.TrimSpace(entry.TurnID) == "" {
		return nil, nil
	}
	rows, err := queryAITurnRows(ctx, scope, aiTurnQuery{
		turnID: entry.TurnID,
		limit:  1,
	})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

func replaceAITurnRef(ctx context.Context, scope *portalScope, turnID, refKind, refValue string) error {
	if scope == nil || turnID == "" || refKind == "" {
		return nil
	}
	if _, err := scope.db.Exec(ctx, `
		DELETE FROM `+aiTurnRefsTable+`
		WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3 AND turn_id=$4 AND ref_kind=$5
	`, scope.bridgeID, scope.portalID, scope.portalReceiver, turnID, refKind); err != nil {
		return err
	}
	if strings.TrimSpace(refValue) == "" {
		return nil
	}
	_, err := scope.db.Exec(ctx, `
		INSERT INTO `+aiTurnRefsTable+` (
			bridge_id, portal_id, portal_receiver, ref_kind, ref_value, turn_id
		) VALUES ($1, $2, $3, $4, $5, $6)
	`, scope.bridgeID, scope.portalID, scope.portalReceiver, refKind, refValue, turnID)
	return err
}
