package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
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
	key := networkid.PortalKey{
		ID:       networkid.PortalID(portalID),
		Receiver: s.client.UserLogin.ID,
	}
	portal, err := s.client.UserLogin.Bridge.GetPortalByKey(ctx, key)
	if err != nil {
		return nil, err
	}
	meta := portalMeta(portal)
	meta.InternalRoomKind = internalRoomKind
	portal.RoomType = database.RoomTypeDM
	portal.Name = strings.TrimSpace(displayName)
	portal.NameSet = portal.Name != ""
	portal.Topic = ""
	portal.TopicSet = false
	setPortalResolvedTarget(portal, meta, s.client.agentUserID(normalizeAgentID(agentID)))
	if err := s.client.savePortal(ctx, portal, "named room setup"); err != nil {
		return nil, err
	}
	return s.client.ensurePortalRoom(ctx, ensurePortalRoomParams{Portal: portal})
}
