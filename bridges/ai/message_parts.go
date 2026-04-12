package ai

import (
	"context"
	"fmt"

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
	part, err := oc.UserLogin.Bridge.DB.Message.GetPartByMXID(ctx, eventID)
	if err != nil || part == nil {
		return part, err
	}
	if part.Room != portal.PortalKey {
		return nil, fmt.Errorf("message %s is not in portal %v", eventID, portal.PortalKey)
	}
	return part, nil
}
