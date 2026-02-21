package connector

import integrationcron "github.com/beeper/ai-bridge/pkg/integrations/cron"

func buildCronMessage(jobID, jobName, message, timezone string) string {
	return integrationcron.BuildCronMessage(jobID, jobName, message, timezone)
}

func formatCronTime(timezone string) string {
	return integrationcron.FormatCronTime(timezone)
}

func wrapSafeExternalPrompt(message string) string {
	return integrationcron.WrapSafeExternalPrompt(message)
}
