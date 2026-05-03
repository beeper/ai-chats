package ai

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func deleteAITurnByExternalRefByScope(
	ctx context.Context,
	scope *portalScope,
	messageID networkid.MessageID,
	eventID id.EventID,
) error {
	if scope == nil {
		return nil
	}
	record, err := loadAITurnByRefByScope(ctx, scope, messageID, eventID)
	if err != nil || record == nil {
		return err
	}
	return scope.db.DoTxn(ctx, nil, func(ctx context.Context) error {
		if _, err := scope.db.Exec(ctx, `
			DELETE FROM `+aiTurnRefsTable+`
			WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3 AND turn_id=$4
		`, scope.bridgeID, scope.portalID, scope.portalReceiver, record.TurnID); err != nil {
			return err
		}
		_, err := scope.db.Exec(ctx, `
			DELETE FROM `+aiTurnsTable+`
			WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3 AND turn_id=$4
		`, scope.bridgeID, scope.portalID, scope.portalReceiver, record.TurnID)
		return err
	})
}

func (oc *AIClient) deleteAITurnByExternalRef(
	ctx context.Context,
	portal *bridgev2.Portal,
	messageID networkid.MessageID,
	eventID id.EventID,
) error {
	return withResolvedPortalScope(ctx, oc, portal, func(ctx context.Context, portal *bridgev2.Portal, scope *portalScope) error {
		return deleteAITurnByExternalRefByScope(ctx, scope, messageID, eventID)
	})
}

func deleteAITurnRecordByScope(ctx context.Context, scope *portalScope, record *aiTurnRecord) error {
	if scope == nil || record == nil || strings.TrimSpace(record.TurnID) == "" {
		return nil
	}
	return scope.db.DoTxn(ctx, nil, func(ctx context.Context) error {
		if _, err := scope.db.Exec(ctx, `
			DELETE FROM `+aiTurnRefsTable+`
			WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3 AND turn_id=$4
		`, scope.bridgeID, scope.portalID, scope.portalReceiver, record.TurnID); err != nil {
			return err
		}
		_, err := scope.db.Exec(ctx, `
			DELETE FROM `+aiTurnsTable+`
			WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3 AND turn_id=$4
		`, scope.bridgeID, scope.portalID, scope.portalReceiver, record.TurnID)
		return err
	})
}

func deleteAINextAssistantTurnAfterExternalRefByScope(
	ctx context.Context,
	scope *portalScope,
	messageID networkid.MessageID,
	eventID id.EventID,
) ([]*aiTurnRecord, error) {
	if scope == nil {
		return nil, nil
	}
	record, err := loadAITurnByRefByScope(ctx, scope, messageID, eventID)
	if err != nil || record == nil {
		return nil, err
	}
	rows, err := queryAITurnRows(ctx, scope, aiTurnQuery{
		contextEpoch:         record.ContextEpoch,
		hasContextEpoch:      true,
		roles:                []string{string(PromptRoleAssistant)},
		minSequenceExclusive: record.Sequence,
	})
	if err != nil || len(rows) == 0 {
		return rows, err
	}
	next := rows[len(rows)-1]
	if err := deleteAITurnRecordByScope(ctx, scope, next); err != nil {
		return nil, err
	}
	return []*aiTurnRecord{next}, nil
}

func (oc *AIClient) deleteAINextAssistantTurnAfterExternalRef(
	ctx context.Context,
	portal *bridgev2.Portal,
	messageID networkid.MessageID,
	eventID id.EventID,
) ([]*aiTurnRecord, error) {
	return withResolvedPortalScopeValue(ctx, oc, portal, func(ctx context.Context, portal *bridgev2.Portal, scope *portalScope) ([]*aiTurnRecord, error) {
		return deleteAINextAssistantTurnAfterExternalRefByScope(ctx, scope, messageID, eventID)
	})
}

func deleteAITurnsAfterExternalRefByScope(
	ctx context.Context,
	scope *portalScope,
	messageID networkid.MessageID,
	eventID id.EventID,
) ([]*aiTurnRecord, error) {
	if scope == nil {
		return nil, nil
	}
	record, err := loadAITurnByRefByScope(ctx, scope, messageID, eventID)
	if err != nil || record == nil {
		return nil, err
	}
	rows, err := queryAITurnRows(ctx, scope, aiTurnQuery{
		contextEpoch:         record.ContextEpoch,
		hasContextEpoch:      true,
		minSequenceExclusive: record.Sequence,
	})
	if err != nil || len(rows) == 0 {
		return rows, err
	}
	return rows, scope.db.DoTxn(ctx, nil, func(ctx context.Context) error {
		if _, err := scope.db.Exec(ctx, `
			DELETE FROM `+aiTurnRefsTable+`
			WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3
			  AND turn_id IN (
				SELECT turn_id FROM `+aiTurnsTable+`
				WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3
				  AND context_epoch=$4 AND sequence>$5
			  )
		`, scope.bridgeID, scope.portalID, scope.portalReceiver, record.ContextEpoch, record.Sequence); err != nil {
			return err
		}
		_, err := scope.db.Exec(ctx, `
			DELETE FROM `+aiTurnsTable+`
			WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3
			  AND context_epoch=$4 AND sequence>$5
		`, scope.bridgeID, scope.portalID, scope.portalReceiver, record.ContextEpoch, record.Sequence)
		return err
	})
}

func (oc *AIClient) deleteAITurnsAfterExternalRef(
	ctx context.Context,
	portal *bridgev2.Portal,
	messageID networkid.MessageID,
	eventID id.EventID,
) ([]*aiTurnRecord, error) {
	return withResolvedPortalScopeValue(ctx, oc, portal, func(ctx context.Context, portal *bridgev2.Portal, scope *portalScope) ([]*aiTurnRecord, error) {
		return deleteAITurnsAfterExternalRefByScope(ctx, scope, messageID, eventID)
	})
}

func deleteAITurnsForPortal(ctx context.Context, portal *bridgev2.Portal) {
	portal, scope, err := resolveAIDBPortalScope(ctx, nil, portal)
	if err != nil || scope == nil {
		return
	}
	log := portal.Bridge.Log
	execDelete(ctx, scope.db, &log,
		`DELETE FROM `+aiTurnRefsTable+` WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3`,
		scope.bridgeID, scope.portalID, scope.portalReceiver,
	)
	execDelete(ctx, scope.db, &log,
		`DELETE FROM `+aiTurnsTable+` WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3`,
		scope.bridgeID, scope.portalID, scope.portalReceiver,
	)
}
