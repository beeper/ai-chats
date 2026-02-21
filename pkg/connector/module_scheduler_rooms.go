package connector

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/ai-bridge/pkg/agents"
	integrationcron "github.com/beeper/ai-bridge/pkg/integrations/cron"
)

func cronPortalKey(loginID networkid.UserLoginID, agentID, jobID string) networkid.PortalKey {
	return networkid.PortalKey{
		ID:       networkid.PortalID(fmt.Sprintf("openai:%s:cron:%s:%s", loginID, url.PathEscape(agentID), url.PathEscape(jobID))),
		Receiver: loginID,
	}
}

func (oc *AIClient) getOrCreateCronRoom(ctx context.Context, agentID, jobID, jobName string) (*bridgev2.Portal, error) {
	if oc == nil || oc.UserLogin == nil {
		return nil, errors.New("missing login")
	}
	room, err := integrationcron.GetOrCreateCronRoom(ctx, agentID, jobID, jobName, integrationcron.RoomResolverDeps{
		DefaultAgentID: agents.DefaultAgentID,
		ResolveRoom: func(ctx context.Context, normalizedAgentID, normalizedJobID string) (any, string, error) {
			portalKey := cronPortalKey(oc.UserLogin.ID, normalizedAgentID, normalizedJobID)
			portal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
			if err != nil {
				return nil, "", err
			}
			return portal, portal.MXID.String(), nil
		},
		CreateRoom: func(ctx context.Context, normalizedAgentID, normalizedJobID, displayName string) (any, error) {
			portalKey := cronPortalKey(oc.UserLogin.ID, normalizedAgentID, normalizedJobID)
			portal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
			if err != nil {
				return nil, fmt.Errorf("failed to get portal: %w", err)
			}
			portal.Metadata = &PortalMetadata{
				IsCronRoom: true,
				CronJobID:  normalizedJobID,
				AgentID:    normalizedAgentID,
			}
			portal.Name = displayName
			portal.NameSet = true
			if err := portal.Save(ctx); err != nil {
				return nil, fmt.Errorf("failed to save portal: %w", err)
			}
			chatInfo := &bridgev2.ChatInfo{Name: &portal.Name}
			if err := portal.CreateMatrixRoom(ctx, oc.UserLogin, chatInfo); err != nil {
				return nil, fmt.Errorf("failed to create Matrix room: %w", err)
			}
			return portal, nil
		},
		LogCreated: func(ctx context.Context, agentID, jobID string, room any) {
			portal, _ := room.(*bridgev2.Portal)
			if portal == nil {
				return
			}
			oc.loggerForContext(ctx).Info().Str("agent_id", strings.TrimSpace(agentID)).Str("job_id", strings.TrimSpace(jobID)).Stringer("portal", portal.PortalKey).Msg("Created cron room")
		},
	})
	if err != nil {
		return nil, err
	}
	portal, _ := room.(*bridgev2.Portal)
	if portal == nil {
		return nil, errors.New("failed to resolve cron room")
	}
	return portal, nil
}
