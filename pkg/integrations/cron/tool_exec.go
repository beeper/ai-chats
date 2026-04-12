package cron

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agenttools "github.com/beeper/agentremote/pkg/agents/tools"
	"github.com/beeper/agentremote/pkg/shared/maputil"
)

type ToolCreateContext struct {
	AgentID        string
	SourceInternal bool
	SourceRoomID   string
}

type ReminderContextLine struct {
	Role string
	Text string
}

type ToolExecDeps struct {
	Status func() (enabled bool, backend string, jobCount int, nextRun *int64, err error)
	List   func(includeDisabled bool) ([]Job, error)
	Add    func(input JobCreate) (Job, error)
	Update func(id string, patch JobPatch) (Job, error)
	Remove func(id string) (bool, error)
	Run    func(id string) (bool, string, error)

	NowMs                func() int64
	ResolveCreateContext func() ToolCreateContext
	ResolveReminderLines func(count int) []ReminderContextLine
	ValidateDeliveryTo   func(to string) error
}

const (
	reminderContextMessagesMax   = 10
	reminderContextPerMessageMax = 220
	reminderContextTotalMax      = 700
	reminderContextMarker        = "\n\nRecent context:\n"
)

func ExecuteTool(ctx context.Context, args map[string]any, deps ToolExecDeps) (string, error) {
	action := strings.ToLower(maputil.StringArgDefault(args, "action", ""))
	if action == "" {
		return agenttools.JSONResult(map[string]any{
			"status": "error",
			"error":  "action is required",
		}).Text(), nil
	}

	nowMs := time.Now().UnixMilli()
	if deps.NowMs != nil {
		nowMs = deps.NowMs()
	}

	switch action {
	case "status":
		if deps.Status == nil {
			return errorJSON("cron status unavailable"), nil
		}
		enabled, backend, jobCount, nextRun, err := deps.Status()
		if err != nil {
			return errorJSON(err.Error()), nil
		}
		var nextRunAtMs any
		if nextRun != nil {
			nextRunAtMs = *nextRun
		}
		out := map[string]any{
			"enabled":     enabled,
			"backend":     backend,
			"jobs":        jobCount,
			"nextRunAtMs": nextRunAtMs,
		}
		return agenttools.JSONResult(out).Text(), nil
	case "list":
		if deps.List == nil {
			return errorJSON("cron list unavailable"), nil
		}
		includeDisabled := agenttools.ReadBool(args, "includeDisabled", false)
		jobs, err := deps.List(includeDisabled)
		if err != nil {
			return errorJSON(err.Error()), nil
		}
		return agenttools.JSONResult(map[string]any{"jobs": jobs}).Text(), nil
	case "add":
		if deps.Add == nil {
			return errorJSON("cron add unavailable"), nil
		}
		jobRaw := readJobInput(args)
		if jobRaw == nil {
			return errorJSON("job required"), nil
		}
		jobInput, err := NormalizeJobCreateRaw(jobRaw)
		if err != nil {
			return errorJSON(err.Error()), nil
		}
		injectToolContext(&jobInput, deps.ResolveCreateContext)
		if jobInput.Delivery != nil && strings.EqualFold(strings.TrimSpace(string(jobInput.Delivery.Mode)), "announce") {
			if deps.ValidateDeliveryTo != nil {
				if err := deps.ValidateDeliveryTo(jobInput.Delivery.To); err != nil {
					return errorJSON(err.Error()), nil
				}
			}
		}
		if strings.TrimSpace(jobInput.Payload.Kind) == "" {
			return errorJSON("payload.kind is required"), nil
		}
		if result := ValidateSchedule(jobInput.Schedule); !result.Ok {
			return errorJSON(result.Message), nil
		}
		if result := ValidateScheduleTimestamp(jobInput.Schedule, nowMs); !result.Ok {
			return errorJSON(result.Message), nil
		}
		contextMessages := agenttools.ReadIntDefault(args, "contextMessages", 0)
		if contextMessages > 0 {
			var lines []ReminderContextLine
			if deps.ResolveReminderLines != nil {
				lines = deps.ResolveReminderLines(contextMessages)
			}
			injectReminderContext(&jobInput, lines, contextMessages)
		}
		job, err := deps.Add(jobInput)
		if err != nil {
			return errorJSON(err.Error()), nil
		}
		return agenttools.JSONResult(job).Text(), nil
	case "update":
		if deps.Update == nil {
			return errorJSON("cron update unavailable"), nil
		}
		jobID := readJobID(args)
		if jobID == "" {
			return errorJSON("jobId required"), nil
		}
		rawPatch := selectPatch(args)
		if rawPatch == nil {
			return errorJSON("patch required"), nil
		}
		patch, err := NormalizeJobPatchRaw(rawPatch)
		if err != nil {
			return errorJSON(err.Error()), nil
		}
		if patch.Delivery != nil && patch.Delivery.To != nil && deps.ValidateDeliveryTo != nil {
			if err := deps.ValidateDeliveryTo(*patch.Delivery.To); err != nil {
				return errorJSON(err.Error()), nil
			}
		}
		if patch.Schedule != nil {
			if result := ValidateSchedule(*patch.Schedule); !result.Ok {
				return errorJSON(result.Message), nil
			}
			if result := ValidateScheduleTimestamp(*patch.Schedule, nowMs); !result.Ok {
				return errorJSON(result.Message), nil
			}
		}
		job, err := deps.Update(jobID, patch)
		if err != nil {
			return errorJSON(err.Error()), nil
		}
		return agenttools.JSONResult(job).Text(), nil
	case "remove":
		if deps.Remove == nil {
			return errorJSON("cron remove unavailable"), nil
		}
		jobID := readJobID(args)
		if jobID == "" {
			return errorJSON("jobId required"), nil
		}
		removed, err := deps.Remove(jobID)
		if err != nil {
			return errorJSON(err.Error()), nil
		}
		return agenttools.JSONResult(map[string]any{
			"ok":      true,
			"removed": removed,
		}).Text(), nil
	case "run":
		if deps.Run == nil {
			return errorJSON("cron run unavailable"), nil
		}
		jobID := readJobID(args)
		if jobID == "" {
			return errorJSON("jobId required"), nil
		}
		ran, reason, err := deps.Run(jobID)
		if err != nil {
			return errorJSON(err.Error()), nil
		}
		out := map[string]any{
			"ok":  true,
			"ran": ran,
		}
		if reason != "" {
			out["reason"] = reason
		}
		return agenttools.JSONResult(out).Text(), nil
	default:
		return errorJSON(fmt.Sprintf("unknown action: %s", action)), nil
	}
}

func readJobID(args map[string]any) string {
	if args == nil {
		return ""
	}
	return strings.TrimSpace(agenttools.ReadStringDefault(args, "jobId", ""))
}

func selectPatch(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	if raw, ok := args["patch"]; ok {
		if patch, ok := raw.(map[string]any); ok {
			return patch
		}
	}
	return nil
}

func readJobInput(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	if raw, ok := args["job"].(map[string]any); ok {
		return raw
	}
	return nil
}

func injectToolContext(job *JobCreate, resolve func() ToolCreateContext) {
	if job == nil {
		return
	}
	tc := ToolCreateContext{}
	if resolve != nil {
		tc = resolve()
	}
	if job.AgentID == nil || strings.TrimSpace(*job.AgentID) == "" {
		agentID := strings.TrimSpace(tc.AgentID)
		if agentID == "" {
			agentID = "default"
		}
		job.AgentID = &agentID
	}
	if job.Delivery != nil &&
		job.Delivery.Mode == DeliveryAnnounce &&
		strings.TrimSpace(job.Delivery.To) == "" &&
		!tc.SourceInternal &&
		strings.TrimSpace(tc.SourceRoomID) != "" {
		job.Delivery.To = strings.TrimSpace(tc.SourceRoomID)
	}
}

func injectReminderContext(job *JobCreate, lines []ReminderContextLine, count int) {
	if job == nil {
		return
	}
	kind := strings.ToLower(strings.TrimSpace(job.Payload.Kind))
	if kind != "agentturn" {
		return
	}
	text := strings.TrimSpace(job.Payload.Message)
	if text == "" {
		return
	}
	contextLines := buildReminderContextLines(lines, count)
	if len(contextLines) == 0 {
		return
	}
	baseText := stripExistingReminderContext(text)
	withContext := baseText + reminderContextMarker + strings.Join(contextLines, "\n")
	job.Payload.Message = withContext
}

func stripExistingReminderContext(text string) string {
	idx := strings.Index(text, reminderContextMarker)
	if idx == -1 {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(text[:idx])
}

func buildReminderContextLines(lines []ReminderContextLine, count int) []string {
	if count <= 0 {
		return nil
	}
	maxMessages := min(count, reminderContextMessagesMax)
	if len(lines) == 0 {
		return nil
	}

	entries := make([]ReminderContextLine, 0, len(lines))
	for _, line := range lines {
		role := strings.ToLower(strings.TrimSpace(line.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		text := normalizeContextText(line.Text)
		if text == "" {
			continue
		}
		entries = append(entries, ReminderContextLine{Role: role, Text: text})
	}
	if len(entries) == 0 {
		return nil
	}
	if len(entries) > maxMessages {
		entries = entries[len(entries)-maxMessages:]
	}

	out := make([]string, 0, len(entries))
	total := 0
	for _, entry := range entries {
		label := roleLabel(entry.Role)
		text := truncateContextText(entry.Text, reminderContextPerMessageMax)
		line := fmt.Sprintf("- %s: %s", label, text)
		total += len(line)
		if total > reminderContextTotalMax {
			break
		}
		out = append(out, line)
	}
	return out
}

func roleLabel(role string) string {
	if role == "assistant" {
		return "Assistant"
	}
	return "User"
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

func errorJSON(msg string) string {
	trimmed := strings.TrimSpace(msg)
	if trimmed == "" {
		trimmed = "unknown error"
	}
	return agenttools.JSONResult(map[string]any{
		"status": "error",
		"error":  trimmed,
	}).Text()
}

func ValidateDeliveryTo(to string) error {
	trimmed := strings.TrimSpace(to)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "@") {
		return errors.New("delivery.to must be a Matrix room id like !room:server (not a user id), omit delivery.to to route to last active room / default chat")
	}
	if !strings.HasPrefix(trimmed, "!") {
		return errors.New("delivery.to must be a Matrix room id like !room:server")
	}
	return nil
}
