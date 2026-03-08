package connector

import (
	"context"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
)

func (s *schedulerRuntime) ensureCronRoomLocked(ctx context.Context, record *scheduledCronJob) error {
	if record == nil {
		return nil
	}
	portalID := fmt.Sprintf("cron:%s:%s", normalizeAgentID(record.Job.AgentID), strings.TrimSpace(record.Job.ID))
	portal, err := s.getOrCreateScheduledPortal(ctx, portalID, fmt.Sprintf("Cron: %s", strings.TrimSpace(record.Job.Name)), func(meta *PortalMetadata) {
		if meta.ModuleMeta == nil {
			meta.ModuleMeta = make(map[string]any)
		}
		meta.ModuleMeta["cron"] = map[string]any{
			"is_internal_room": true,
			"backend":          "hungry",
			"job_id":           record.Job.ID,
			"revision":         record.Revision,
			"managed":          true,
		}
	})
	if err != nil {
		return err
	}
	portal.OtherUserID = agentUserID(normalizeAgentID(record.Job.AgentID))
	if err := portal.Save(ctx); err != nil {
		return err
	}
	record.RoomID = portal.MXID.String()
	return nil
}

func (s *schedulerRuntime) ensureHeartbeatRoomLocked(ctx context.Context, state *managedHeartbeatState) error {
	if state == nil {
		return nil
	}
	portalID := fmt.Sprintf("heartbeat:%s", normalizeAgentID(state.AgentID))
	portal, err := s.getOrCreateScheduledPortal(ctx, portalID, fmt.Sprintf("Heartbeat: %s", state.AgentID), func(meta *PortalMetadata) {
		if meta.ModuleMeta == nil {
			meta.ModuleMeta = make(map[string]any)
		}
		meta.ModuleMeta["heartbeat"] = map[string]any{
			"is_internal_room": true,
			"backend":          "hungry",
			"agent_id":         state.AgentID,
			"revision":         state.Revision,
			"managed":          true,
		}
	})
	if err != nil {
		return err
	}
	portal.OtherUserID = agentUserID(normalizeAgentID(state.AgentID))
	if err := portal.Save(ctx); err != nil {
		return err
	}
	state.RoomID = portal.MXID.String()
	return nil
}

func (s *schedulerRuntime) getOrCreateScheduledPortal(ctx context.Context, portalID, displayName string, setup func(meta *PortalMetadata)) (*bridgev2.Portal, error) {
	key := portalKeyFromParts(s.client, portalID, string(s.client.UserLogin.ID))
	portal, err := s.client.UserLogin.Bridge.GetPortalByKey(ctx, key)
	if err != nil {
		return nil, err
	}
	if portal.MXID != "" {
		meta := portalMeta(portal)
		if meta == nil {
			meta = &PortalMetadata{}
			portal.Metadata = meta
		}
		if setup != nil {
			setup(meta)
		}
		s.client.savePortalQuiet(ctx, portal, "scheduler metadata update")
		return portal, nil
	}
	meta := &PortalMetadata{}
	if setup != nil {
		setup(meta)
	}
	portal.Metadata = meta
	portal.Name = displayName
	portal.NameSet = true
	if err := portal.Save(ctx); err != nil {
		return nil, err
	}
	chatInfo := &bridgev2.ChatInfo{Name: &portal.Name}
	if err := portal.CreateMatrixRoom(ctx, s.client.UserLogin, chatInfo); err != nil {
		return nil, err
	}
	return portal, nil
}
