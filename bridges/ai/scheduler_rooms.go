package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	bridgesdk "github.com/beeper/agentremote/sdk"
)

func (s *schedulerRuntime) ensureScheduledRoomLocked(ctx context.Context, portalID, displayName, agentID string, moduleMeta map[string]any) (string, error) {
	portal, err := s.getOrCreateScheduledPortal(ctx, portalID, displayName, func(meta *PortalMetadata) {
		for k, v := range moduleMeta {
			meta.SetModuleMeta(k, v)
		}
	})
	if err != nil {
		return "", err
	}
	portal.OtherUserID = s.client.agentUserID(normalizeAgentID(agentID))
	if err := portal.Save(ctx); err != nil {
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
	roomID, err := s.ensureScheduledRoomLocked(ctx, portalID, displayName, record.Job.AgentID, map[string]any{
		"cron": map[string]any{
			"is_internal_room": true,
			"backend":          "hungry",
			"job_id":           record.Job.ID,
			"revision":         record.Revision,
			"managed":          true,
		},
	})
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
	roomID, err := s.ensureScheduledRoomLocked(ctx, portalID, displayName, state.AgentID, map[string]any{
		"heartbeat": map[string]any{
			"is_internal_room": true,
			"backend":          "hungry",
			"agent_id":         state.AgentID,
			"revision":         state.Revision,
			"managed":          true,
		},
	})
	if err != nil {
		return err
	}
	state.RoomID = roomID
	return nil
}

func (s *schedulerRuntime) getOrCreateScheduledPortal(ctx context.Context, portalID, displayName string, setup func(meta *PortalMetadata)) (*bridgev2.Portal, error) {
	if s == nil || s.client == nil || s.client.UserLogin == nil || s.client.UserLogin.Bridge == nil {
		return nil, errors.New("scheduler client is not available")
	}
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
	chatInfo := &bridgev2.ChatInfo{Name: &portal.Name}
	_, err = bridgesdk.EnsurePortalLifecycle(ctx, bridgesdk.PortalLifecycleOptions{
		Login:             s.client.UserLogin,
		Portal:            portal,
		ChatInfo:          chatInfo,
		SaveBeforeCreate:  true,
		AIRoomKind:        integrationPortalAIKind(meta),
		ForceCapabilities: true,
		RefreshExtra: func(ctx context.Context, portal *bridgev2.Portal) {
			s.client.BroadcastCommandDescriptions(ctx, portal)
		},
	})
	if err != nil {
		return nil, err
	}
	return portal, nil
}
