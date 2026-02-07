package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/agents"
)

func (oc *AIClient) getOrCreateCronRoom(ctx context.Context, agentID, jobID, jobName string) (*bridgev2.Portal, error) {
	if oc == nil || oc.UserLogin == nil {
		return nil, errors.New("missing login")
	}
	trimmedAgent := strings.TrimSpace(agentID)
	if trimmedAgent == "" {
		trimmedAgent = agents.DefaultAgentID
	}
	trimmedJob := strings.TrimSpace(jobID)
	if trimmedJob == "" {
		return nil, errors.New("jobID required")
	}
	portalKey := cronPortalKey(oc.UserLogin.ID, trimmedAgent, trimmedJob)
	portal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get portal: %w", err)
	}
	if portal.MXID != "" {
		return portal, nil
	}

	name := strings.TrimSpace(jobName)
	if name == "" {
		name = trimmedJob
	}
	display := fmt.Sprintf("Cron: %s", name)
	portal.Metadata = &PortalMetadata{
		IsCronRoom: true,
		CronJobID:  trimmedJob,
		AgentID:    trimmedAgent,
	}
	portal.Name = display
	portal.NameSet = true
	if err := portal.Save(ctx); err != nil {
		return nil, fmt.Errorf("failed to save portal: %w", err)
	}

	chatInfo := &bridgev2.ChatInfo{Name: &portal.Name}
	if err := portal.CreateMatrixRoom(ctx, oc.UserLogin, chatInfo); err != nil {
		return nil, fmt.Errorf("failed to create Matrix room: %w", err)
	}
	oc.loggerForContext(ctx).Info().Str("agent_id", trimmedAgent).Str("job_id", trimmedJob).Stringer("portal", portal.PortalKey).Msg("Created cron room")
	return portal, nil
}
