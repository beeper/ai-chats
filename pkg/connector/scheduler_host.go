package connector

import (
	"context"
	"fmt"

	integrationcron "github.com/beeper/ai-bridge/pkg/integrations/cron"
)

func (h *runtimeIntegrationHost) CronStatus(ctx context.Context) (bool, string, int, *int64, error) {
	if h == nil || h.client == nil || h.client.scheduler == nil {
		return false, "", 0, nil, fmt.Errorf("scheduler not available")
	}
	return h.client.scheduler.CronStatus(ctx)
}

func (h *runtimeIntegrationHost) CronList(ctx context.Context, includeDisabled bool) ([]integrationcron.Job, error) {
	if h == nil || h.client == nil || h.client.scheduler == nil {
		return nil, fmt.Errorf("scheduler not available")
	}
	return h.client.scheduler.CronList(ctx, includeDisabled)
}

func (h *runtimeIntegrationHost) CronAdd(ctx context.Context, input integrationcron.JobCreate) (integrationcron.Job, error) {
	if h == nil || h.client == nil || h.client.scheduler == nil {
		return integrationcron.Job{}, fmt.Errorf("scheduler not available")
	}
	return h.client.scheduler.CronAdd(ctx, input)
}

func (h *runtimeIntegrationHost) CronUpdate(ctx context.Context, jobID string, patch integrationcron.JobPatch) (integrationcron.Job, error) {
	if h == nil || h.client == nil || h.client.scheduler == nil {
		return integrationcron.Job{}, fmt.Errorf("scheduler not available")
	}
	return h.client.scheduler.CronUpdate(ctx, jobID, patch)
}

func (h *runtimeIntegrationHost) CronRemove(ctx context.Context, jobID string) (bool, error) {
	if h == nil || h.client == nil || h.client.scheduler == nil {
		return false, fmt.Errorf("scheduler not available")
	}
	return h.client.scheduler.CronRemove(ctx, jobID)
}

func (h *runtimeIntegrationHost) CronRun(ctx context.Context, jobID string) (bool, string, error) {
	if h == nil || h.client == nil || h.client.scheduler == nil {
		return false, "", fmt.Errorf("scheduler not available")
	}
	return h.client.scheduler.CronRun(ctx, jobID)
}
