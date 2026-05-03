package ai

import (
	"context"
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
	part, err := oc.UserLogin.Bridge.DB.Message.GetPartByMXID(ctx, eventID)
	if err != nil {
		return nil, fmt.Errorf("message lookup failed for %s in portal %s/%s: %w",
			eventID, strings.TrimSpace(string(portal.PortalKey.ID)), strings.TrimSpace(string(portal.PortalKey.Receiver)), err)
	}
	if part == nil || part.Room != portal.PortalKey {
		return nil, nil
	}
	return part, nil
}
