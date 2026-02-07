package cron

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// normalizeRequiredName trims and validates a job name.
func normalizeRequiredName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("cron job name is required")
	}
	return name, nil
}

func normalizeOptionalText(raw *string) string {
	if raw == nil {
		return ""
	}
	return strings.TrimSpace(*raw)
}

func normalizeOptionalAgentID(raw *string) string {
	if raw == nil {
		return ""
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return ""
	}
	return sanitizeAgentID(trimmed)
}

var (
	agentIDValidRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	agentIDInvalidChars = regexp.MustCompile(`[^a-z0-9_-]+`)
	agentIDLeadingDash  = regexp.MustCompile(`^-+`)
	agentIDTrailingDash = regexp.MustCompile(`-+$`)
)

const cronDefaultAgentID = "main"

func sanitizeAgentID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return cronDefaultAgentID
	}
	lowered := strings.ToLower(trimmed)
	if agentIDValidRe.MatchString(lowered) {
		return lowered
	}
	cleaned := agentIDInvalidChars.ReplaceAllString(lowered, "-")
	cleaned = agentIDLeadingDash.ReplaceAllString(cleaned, "")
	cleaned = agentIDTrailingDash.ReplaceAllString(cleaned, "")
	if len(cleaned) > 64 {
		cleaned = cleaned[:64]
	}
	if cleaned == "" {
		return cronDefaultAgentID
	}
	return cleaned
}

func truncateText(input string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(input) <= maxLen {
		return input
	}
	if maxLen == 1 {
		return "…"
	}
	return strings.TrimSpace(input[:maxLen-1]) + "…"
}

// inferLegacyName mirrors OpenClaw's fallback naming.
func inferLegacyName(job *CronJobCreate) string {
	if job == nil {
		return "Cron job"
	}
	text := ""
	switch strings.ToLower(job.Payload.Kind) {
	case "systemevent":
		text = job.Payload.Text
	case "agentturn":
		text = job.Payload.Message
	}
	firstLine := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			firstLine = line
			break
		}
	}
	if firstLine != "" {
		return truncateText(firstLine, 60)
	}
	switch strings.ToLower(job.Schedule.Kind) {
	case "cron":
		if strings.TrimSpace(job.Schedule.Expr) != "" {
			return "Cron: " + truncateText(strings.TrimSpace(job.Schedule.Expr), 52)
		}
	case "every":
		if job.Schedule.EveryMs > 0 {
			return fmt.Sprintf("Every: %dms", job.Schedule.EveryMs)
		}
	case "at":
		return "One-shot"
	}
	return "Cron job"
}

func normalizePayloadToSystemText(payload CronPayload) string {
	if strings.EqualFold(payload.Kind, "systemEvent") {
		return strings.TrimSpace(payload.Text)
	}
	return strings.TrimSpace(payload.Message)
}

func resolveJobPayloadTextForMain(job CronJob) (string, string) {
	if strings.EqualFold(job.Payload.Kind, "systemEvent") {
		text := normalizePayloadToSystemText(job.Payload)
		if text == "" {
			return "", "main job requires non-empty systemEvent text"
		}
		return text, ""
	}
	return "", `main job requires payload.kind="systemEvent"`
}
