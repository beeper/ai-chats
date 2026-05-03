package ai

import (
	"context"

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
