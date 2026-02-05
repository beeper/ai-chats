package opencodebridge

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
)

func (b *Bridge) queueRemoteEvent(ev bridgev2.RemoteEvent) {
	if b == nil || b.host == nil || ev == nil {
		return
	}
	login := b.host.Login()
	if login == nil {
		return
	}
	login.QueueRemoteEvent(ev)
}

func (b *Bridge) portalMeta(portal *bridgev2.Portal) *PortalMeta {
	if b == nil || b.host == nil || portal == nil {
		return nil
	}
	meta := b.host.PortalMeta(portal)
	if meta == nil {
		meta = &PortalMeta{}
	}
	return meta
}

func (b *Bridge) listAllChatPortals(ctx context.Context) ([]*bridgev2.Portal, error) {
	if b == nil || b.host == nil {
		return nil, nil
	}
	login := b.host.Login()
	if login == nil || login.Bridge == nil || login.Bridge.DB == nil {
		return nil, nil
	}
	allDBPortals, err := login.Bridge.DB.Portal.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	portals := make([]*bridgev2.Portal, 0)
	for _, dbPortal := range allDBPortals {
		if dbPortal.Receiver != login.ID {
			continue
		}
		portal, err := login.Bridge.GetPortalByKey(ctx, dbPortal.PortalKey)
		if err != nil {
			return nil, err
		}
		if portal != nil {
			portals = append(portals, portal)
		}
	}
	return portals, nil
}
