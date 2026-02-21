package connector

import (
	integrationcron "github.com/beeper/ai-bridge/pkg/integrations/cron"
)

func formatCronStatusText(enabled bool, storePath string, jobCount int, nextWakeAtMs *int64) string {
	return integrationcron.FormatCronStatusText(enabled, storePath, jobCount, nextWakeAtMs)
}

func formatCronJobListText(jobs []integrationcron.Job) string {
	return integrationcron.FormatCronJobListText(jobs)
}

func formatCronRunsText(jobID string, entries []integrationcron.RunLogEntry) string {
	return integrationcron.FormatCronRunsText(jobID, entries)
}
