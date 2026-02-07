package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"
)

func (b *BossStoreAdapter) resolvePortalByRoomID(ctx context.Context, roomID string) (*bridgev2.Portal, error) {
	trimmed := strings.TrimSpace(roomID)
	if trimmed == "" {
		return nil, errors.New("room_id is required")
	}

	if strings.HasPrefix(trimmed, "!") {
		portal, err := b.store.client.UserLogin.Bridge.GetPortalByMXID(ctx, id.RoomID(trimmed))
		if err == nil && portal != nil {
			return portal, nil
		}
	}

	portals, err := b.store.client.listAllChatPortals(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list portals: %w", err)
	}
	for _, portal := range portals {
		if string(portal.PortalKey.ID) == trimmed {
			return portal, nil
		}
	}

	return nil, fmt.Errorf("room '%s' not found", trimmed)
}
