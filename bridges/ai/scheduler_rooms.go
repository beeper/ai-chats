package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
)

func (s *schedulerRuntime) ensureScheduledRoomLocked(
	ctx context.Context,
	portalID, displayName, agentID, internalRoomKind string,
) (string, error) {
	portal, err := s.getOrCreateScheduledPortal(ctx, portalID, displayName, agentID, internalRoomKind)
	if err != nil {
		return "", err
	}
	return portal.MXID.String(), nil
}

func (s *schedulerRuntime) ensureCronRoomLocked(ctx context.Context, record *scheduledCronJob) error {
	if record == nil {
		return nil
	}
	portalID := fmt.Sprintf("cron:%s:%s", normalizeAgentID(record.Job.AgentID), strings.TrimSpace(record.Job.ID))
	displayName := fmt.Sprintf("Cron: %s", strings.TrimSpace(record.Job.Name))
	roomID, err := s.ensureScheduledRoomLocked(ctx, portalID, displayName, record.Job.AgentID, "cron")
	if err != nil {
		return err
	}
	record.RoomID = roomID
	return nil
}

func (s *schedulerRuntime) ensureHeartbeatRoomLocked(ctx context.Context, state *managedHeartbeatState) error {
	if state == nil {
		return nil
	}
	portalID := fmt.Sprintf("heartbeat:%s", normalizeAgentID(state.AgentID))
	displayName := fmt.Sprintf("Heartbeat: %s", state.AgentID)
	roomID, err := s.ensureScheduledRoomLocked(ctx, portalID, displayName, state.AgentID, "heartbeat")
	if err != nil {
		return err
	}
	state.RoomID = roomID
	return nil
}

func (s *schedulerRuntime) getOrCreateScheduledPortal(ctx context.Context, portalID, displayName, agentID, internalRoomKind string) (*bridgev2.Portal, error) {
	if s == nil || s.client == nil || s.client.UserLogin == nil || s.client.UserLogin.Bridge == nil {
		return nil, errors.New("scheduler client is not available")
	}
	key := portalKeyFromParts(s.client, portalID, string(s.client.UserLogin.ID))
	chatName := displayName
	portal, err := s.client.UserLogin.Bridge.GetPortalByKey(ctx, key)
	if err != nil {
		return nil, err
	}
	meta := portalMeta(portal)
	if meta == nil {
		meta = &PortalMetadata{}
		portal.Metadata = meta
	}
	meta.InternalRoomKind = internalRoomKind
	portal.OtherUserID = s.client.agentUserID(normalizeAgentID(agentID))
	s.client.applyPortalRoomName(ctx, portal, displayName)
	if err := portal.Save(ctx); err != nil {
		return nil, err
	}
	err = s.client.materializePortalRoom(ctx, portal, &bridgev2.ChatInfo{Name: &chatName}, portalRoomMaterializeOptions{})
	if err != nil {
		return nil, err
	}
	return portal, nil
}
