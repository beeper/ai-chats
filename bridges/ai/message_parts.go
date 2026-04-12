package ai

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"
)

func (oc *AIClient) loadPortalMessagePartByMXID(
	ctx context.Context,
	portal *bridgev2.Portal,
	eventID id.EventID,
) (*database.Message, error) {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.DB == nil || oc.UserLogin.Bridge.DB.Message == nil {
		return nil, nil
	}
	if portal == nil || eventID == "" {
		return nil, nil
	}
	db := bridgeDBFromPortal(portal)
	if db == nil || portal.Bridge == nil || portal.Bridge.DB == nil {
		return nil, nil
	}
	var rowID int64
	err := db.QueryRow(ctx, `
		SELECT rowid
		FROM message
		WHERE bridge_id=$1 AND mxid=$2 AND room_id=$3 AND room_receiver=$4
		LIMIT 1
	`,
		string(portal.Bridge.DB.BridgeID),
		eventID.String(),
		string(portal.PortalKey.ID),
		string(portal.PortalKey.Receiver),
	).Scan(&rowID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("message lookup failed for %s in portal %s/%s: %w",
			eventID, strings.TrimSpace(string(portal.PortalKey.ID)), strings.TrimSpace(string(portal.PortalKey.Receiver)), err)
	}
	part, err := oc.UserLogin.Bridge.DB.Message.GetByRowID(ctx, rowID)
	if err != nil || part == nil {
		return part, err
	}
	return part, nil
}
