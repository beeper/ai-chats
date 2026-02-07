package connector

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/agents"
	agenttools "github.com/beeper/ai-bridge/pkg/agents/tools"
	"github.com/beeper/ai-bridge/pkg/cron"
)

func executeCron(ctx context.Context, args map[string]any) (string, error) {
	btc := GetBridgeToolContext(ctx)
	if btc == nil || btc.Client == nil {
		return "", errors.New("cron tool requires bridge context")
	}
	client := btc.Client
	if client.cronService == nil {
		return "", errors.New("cron service not available")
	}

	action := strings.ToLower(strings.TrimSpace(agenttools.ReadStringDefault(args, "action", "")))
	if action == "" {
		return agenttools.JSONResult(map[string]any{
			"status": "error",
			"error":  "action is required",
		}).Text(), nil
	}

	switch action {
	case "status":
		enabled, storePath, jobCount, nextWake, err := client.cronService.Status()
		if err != nil {
			return agenttools.JSONResult(map[string]any{
				"status": "error",
				"error":  err.Error(),
			}).Text(), nil
		}
		out := map[string]any{
			"enabled":   enabled,
			"storePath": storePath,
			"jobs":      jobCount,
		}
		if nextWake != nil {
			out["nextWakeAtMs"] = *nextWake
		} else {
			out["nextWakeAtMs"] = nil
		}
		return agenttools.JSONResult(out).Text(), nil
	case "list":
		includeDisabled := agenttools.ReadBool(args, "includeDisabled", false)
		jobs, err := client.cronService.List(includeDisabled)
		if err != nil {
			return agenttools.JSONResult(map[string]any{
				"status": "error",
				"error":  err.Error(),
			}).Text(), nil
		}
		return agenttools.JSONResult(map[string]any{
			"jobs": jobs,
		}).Text(), nil
	case "add":
		jobRaw := readCronJobInput(args)
		if jobRaw == nil {
			return agenttools.JSONResult(map[string]any{
				"status": "error",
				"error":  "job required",
			}).Text(), nil
		}
		jobInput, err := cron.NormalizeCronJobCreateRaw(jobRaw)
		if err != nil {
			return agenttools.JSONResult(map[string]any{
				"status": "error",
				"error":  err.Error(),
			}).Text(), nil
		}
		injectCronContext(&jobInput, btc)
		if strings.TrimSpace(jobInput.Payload.Kind) == "" {
			return agenttools.JSONResult(map[string]any{
				"status": "error",
				"error":  "payload.kind is required",
			}).Text(), nil
		}
		if result := cron.ValidateScheduleTimestamp(jobInput.Schedule, time.Now().UnixMilli()); !result.Ok {
			return agenttools.JSONResult(map[string]any{
				"status": "error",
				"error":  result.Message,
			}).Text(), nil
		}
		contextMessages := agenttools.ReadIntDefault(args, "contextMessages", 0)
		if contextMessages > 0 {
			injectCronReminderContext(&jobInput, btc, contextMessages)
		}
		job, err := client.cronService.Add(jobInput)
		if err != nil {
			return agenttools.JSONResult(map[string]any{
				"status": "error",
				"error":  err.Error(),
			}).Text(), nil
		}
		return agenttools.JSONResult(job).Text(), nil
	case "update":
		jobID := readCronJobID(args)
		if jobID == "" {
			return agenttools.JSONResult(map[string]any{
				"status": "error",
				"error":  "jobId required",
			}).Text(), nil
		}
		rawPatch := selectCronPatch(args)
		if rawPatch == nil {
			return agenttools.JSONResult(map[string]any{
				"status": "error",
				"error":  "patch required",
			}).Text(), nil
		}
		patch, err := cron.NormalizeCronJobPatchRaw(rawPatch)
		if err != nil {
			return agenttools.JSONResult(map[string]any{
				"status": "error",
				"error":  err.Error(),
			}).Text(), nil
		}
		if patch.Schedule != nil {
			if result := cron.ValidateScheduleTimestamp(*patch.Schedule, time.Now().UnixMilli()); !result.Ok {
				return agenttools.JSONResult(map[string]any{
					"status": "error",
					"error":  result.Message,
				}).Text(), nil
			}
		}
		job, err := client.cronService.Update(jobID, patch)
		if err != nil {
			return agenttools.JSONResult(map[string]any{
				"status": "error",
				"error":  err.Error(),
			}).Text(), nil
		}
		return agenttools.JSONResult(job).Text(), nil
	case "remove":
		jobID := readCronJobID(args)
		if jobID == "" {
			return agenttools.JSONResult(map[string]any{
				"status": "error",
				"error":  "jobId required",
			}).Text(), nil
		}
		removed, err := client.cronService.Remove(jobID)
		if err != nil {
			return agenttools.JSONResult(map[string]any{
				"status": "error",
				"error":  err.Error(),
			}).Text(), nil
		}
		return agenttools.JSONResult(map[string]any{
			"ok":      true,
			"removed": removed,
		}).Text(), nil
	case "run":
		jobID := readCronJobID(args)
		if jobID == "" {
			return agenttools.JSONResult(map[string]any{
				"status": "error",
				"error":  "jobId required",
			}).Text(), nil
		}
		ran, reason, err := client.cronService.Run(jobID, "")
		if err != nil {
			return agenttools.JSONResult(map[string]any{
				"status": "error",
				"error":  err.Error(),
			}).Text(), nil
		}
		out := map[string]any{
			"ok":  true,
			"ran": ran,
		}
		if reason != "" {
			out["reason"] = reason
		}
		return agenttools.JSONResult(out).Text(), nil
	case "runs":
		jobID := readCronJobID(args)
		if jobID == "" {
			return agenttools.JSONResult(map[string]any{
				"status": "error",
				"error":  "jobId required",
			}).Text(), nil
		}
		limit := agenttools.ReadIntDefault(args, "limit", 200)
		runs, err := client.readCronRuns(jobID, limit)
		if err != nil {
			return agenttools.JSONResult(map[string]any{
				"status": "error",
				"error":  err.Error(),
			}).Text(), nil
		}
		out := map[string]any{
			"entries": runs,
		}
		return agenttools.JSONResult(out).Text(), nil
	case "wake":
		text := strings.TrimSpace(agenttools.ReadStringDefault(args, "text", ""))
		if text == "" {
			return agenttools.JSONResult(map[string]any{
				"status": "error",
				"error":  "text is required",
			}).Text(), nil
		}
		mode := strings.ToLower(strings.TrimSpace(agenttools.ReadStringDefault(args, "mode", "")))
		if mode != "now" && mode != "next-heartbeat" {
			mode = "next-heartbeat"
		}
		_, err := client.cronService.Wake(mode, text)
		if err != nil {
			return agenttools.JSONResult(map[string]any{
				"status": "error",
				"error":  err.Error(),
			}).Text(), nil
		}
		return agenttools.JSONResult(map[string]any{
			"ok": true,
		}).Text(), nil
	default:
		return agenttools.JSONResult(map[string]any{
			"status": "error",
			"error":  fmt.Sprintf("unknown action: %s", action),
		}).Text(), nil
	}
}

func readCronJobID(args map[string]any) string {
	if args == nil {
		return ""
	}
	return strings.TrimSpace(agenttools.ReadStringDefault(args, "jobId", ""))
}

func selectCronPatch(args map[string]any) any {
	if args == nil {
		return nil
	}
	if raw, ok := args["patch"]; ok {
		if _, ok := raw.(map[string]any); ok {
			return raw
		}
	}
	return nil
}

func readCronJobInput(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	if raw, ok := args["job"].(map[string]any); ok {
		return raw
	}
	return nil
}

func injectCronContext(job *cron.CronJobCreate, btc *BridgeToolContext) {
	if job == nil || btc == nil {
		return
	}
	meta := btc.Meta
	if meta == nil && btc.Portal != nil {
		meta = portalMeta(btc.Portal)
	}
	if job.AgentID == nil || strings.TrimSpace(*job.AgentID) == "" {
		agentID := resolveAgentID(meta)
		if strings.TrimSpace(agentID) == "" {
			agentID = agents.DefaultAgentID
		}
		job.AgentID = &agentID
	}
	// For isolated announce jobs created from a room, pin delivery target to that room.
	// This avoids depending on "last activity" metadata for routing.
	if btc.Portal != nil && btc.Portal.MXID != "" &&
		job.SessionTarget == cron.CronSessionIsolated &&
		strings.EqualFold(strings.TrimSpace(job.Payload.Kind), "agentTurn") {
		if job.Delivery == nil {
			job.Delivery = &cron.CronDelivery{Mode: cron.CronDeliveryAnnounce}
		}
		mode := job.Delivery.Mode
		if strings.TrimSpace(string(mode)) == "" {
			mode = cron.CronDeliveryAnnounce
			job.Delivery.Mode = mode
		}
		channel := strings.ToLower(strings.TrimSpace(job.Delivery.Channel))
		if mode == cron.CronDeliveryAnnounce &&
			strings.TrimSpace(job.Delivery.To) == "" &&
			(channel == "" || channel == "last" || channel == "matrix") {
			job.Delivery.Channel = "matrix"
			job.Delivery.To = btc.Portal.MXID.String()
		}
	}
}

const (
	reminderContextMessagesMax   = 10
	reminderContextPerMessageMax = 220
	reminderContextTotalMax      = 700
	reminderContextMarker        = "\n\nRecent context:\n"
)

func injectCronReminderContext(job *cron.CronJobCreate, btc *BridgeToolContext, count int) {
	if job == nil || btc == nil || btc.Client == nil {
		return
	}
	if strings.ToLower(strings.TrimSpace(job.Payload.Kind)) != "systemevent" {
		return
	}
	text := strings.TrimSpace(job.Payload.Text)
	if text == "" {
		return
	}
	contextLines := buildReminderContextLines(btc, count)
	if len(contextLines) == 0 {
		return
	}
	baseText := stripExistingReminderContext(text)
	job.Payload.Text = baseText + reminderContextMarker + strings.Join(contextLines, "\n")
}

func stripExistingReminderContext(text string) string {
	idx := strings.Index(text, reminderContextMarker)
	if idx == -1 {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(text[:idx])
}

func buildReminderContextLines(btc *BridgeToolContext, count int) []string {
	if btc == nil || btc.Client == nil || btc.Portal == nil {
		return nil
	}
	maxMessages := count
	if maxMessages <= 0 {
		return nil
	}
	if maxMessages > reminderContextMessagesMax {
		maxMessages = reminderContextMessagesMax
	}
	ctx := btc.Client.backgroundContext(context.Background())
	history, err := btc.Client.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, btc.Portal.PortalKey, maxMessages)
	if err != nil || len(history) == 0 {
		return nil
	}
	entries := make([]contextEntry, 0, len(history))
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		meta := messageMeta(msg)
		if meta == nil {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(meta.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		text := normalizeContextText(meta.Body)
		if text == "" {
			continue
		}
		entries = append(entries, contextEntry{role: role, text: text})
	}
	if len(entries) == 0 {
		return nil
	}
	if len(entries) > maxMessages {
		entries = entries[len(entries)-maxMessages:]
	}
	lines := make([]string, 0, len(entries))
	total := 0
	for _, entry := range entries {
		label := "User"
		if entry.role == "assistant" {
			label = "Assistant"
		}
		text := truncateContextText(entry.text, reminderContextPerMessageMax)
		line := fmt.Sprintf("- %s: %s", label, text)
		total += len(line)
		if total > reminderContextTotalMax {
			break
		}
		lines = append(lines, line)
	}
	return lines
}

type contextEntry struct {
	role string
	text string
}

func normalizeContextText(raw string) string {
	return strings.Join(strings.Fields(raw), " ")
}

func truncateContextText(input string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(input)
	if len(runes) <= maxLen {
		return input
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	truncated := strings.TrimRight(string(runes[:maxLen-3]), " ")
	return truncated + "..."
}

func (oc *AIClient) readCronRuns(jobID string, limit int) ([]cron.CronRunLogEntry, error) {
	if oc == nil || oc.cronService == nil {
		return nil, errors.New("cron service not available")
	}
	if limit <= 0 {
		limit = 200
	}
	_, storePath, _, _, err := oc.cronService.Status()
	if err != nil {
		return nil, err
	}
	backend := oc.cronStoreBackend()
	if backend == nil {
		return nil, errors.New("cron store not available")
	}
	trimmed := strings.TrimSpace(jobID)
	if trimmed != "" {
		path := cron.ResolveCronRunLogPath(storePath, trimmed)
		return cron.ReadCronRunLogEntries(context.Background(), backend, path, limit, trimmed)
	}
	entries := make([]cron.CronRunLogEntry, 0)
	store, err := oc.cronTextFSStore()
	if err != nil {
		return entries, nil
	}
	runDir := cron.ResolveCronRunLogDir(storePath)
	files, err := store.ListWithPrefix(context.Background(), runDir)
	if err != nil {
		return entries, nil
	}
	for _, file := range files {
		if !strings.HasSuffix(strings.ToLower(file.Path), ".jsonl") {
			continue
		}
		list := cron.ParseCronRunLogEntries(file.Content, limit, "")
		if len(list) > 0 {
			entries = append(entries, list...)
		}
	}
	slices.SortFunc(entries, func(a, b cron.CronRunLogEntry) int {
		return cmp.Compare(a.TS, b.TS)
	})
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

func cronRunLogEntryFromEvent(evt cron.CronEvent) cron.CronRunLogEntry {
	return cron.CronRunLogEntry{
		TS:          time.Now().UnixMilli(),
		JobID:       evt.JobID,
		Action:      evt.Action,
		Status:      evt.Status,
		Error:       evt.Error,
		Summary:     evt.Summary,
		RunAtMs:     evt.RunAtMs,
		DurationMs:  evt.DurationMs,
		NextRunAtMs: evt.NextRunAtMs,
	}
}
