package cron

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"slices"
	"strings"
	"sync"
)

// CronRunLogEntry mirrors OpenClaw's log format.
type CronRunLogEntry struct {
	TS          int64  `json:"ts"`
	JobID       string `json:"jobId"`
	Action      string `json:"action"`
	Status      string `json:"status,omitempty"`
	Error       string `json:"error,omitempty"`
	Summary     string `json:"summary,omitempty"`
	RunAtMs     int64  `json:"runAtMs,omitempty"`
	DurationMs  int64  `json:"durationMs,omitempty"`
	NextRunAtMs int64  `json:"nextRunAtMs,omitempty"`
}

// ResolveCronRunLogDir returns runs/ directory next to store.
func ResolveCronRunLogDir(storePath string) string {
	trimmed := strings.TrimSpace(storePath)
	if trimmed == "" {
		return "runs"
	}
	dir := path.Dir(trimmed)
	if dir == "." {
		dir = ""
	}
	if dir == "" {
		return "runs"
	}
	return path.Join(dir, "runs")
}

// ResolveCronRunLogPath returns runs/<jobId>.jsonl next to store.
func ResolveCronRunLogPath(storePath, jobID string) string {
	runDir := ResolveCronRunLogDir(storePath)
	return path.Join(runDir, strings.TrimSpace(jobID)+".jsonl")
}

var cronRunLogLocks sync.Map

func cronRunLogLock(path string) *sync.Mutex {
	if path == "" {
		return &sync.Mutex{}
	}
	if val, ok := cronRunLogLocks.Load(path); ok {
		return val.(*sync.Mutex)
	}
	mu := &sync.Mutex{}
	actual, _ := cronRunLogLocks.LoadOrStore(path, mu)
	return actual.(*sync.Mutex)
}

// AppendCronRunLog appends a log entry and prunes if too large.
func AppendCronRunLog(ctx context.Context, backend StoreBackend, path string, entry CronRunLogEntry, maxBytes int64, keepLines int) error {
	if backend == nil {
		return errors.New("cron store backend not configured")
	}
	if maxBytes <= 0 {
		maxBytes = 2_000_000
	}
	if keepLines <= 0 {
		keepLines = 2000
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	lock := cronRunLogLock(path)
	lock.Lock()
	defer lock.Unlock()

	existing := ""
	if data, found, err := backend.Read(ctx, path); err == nil && found {
		existing = string(data)
	}
	builder := strings.Builder{}
	if strings.TrimSpace(existing) != "" {
		builder.WriteString(existing)
		if !strings.HasSuffix(existing, "\n") {
			builder.WriteString("\n")
		}
	}
	builder.Write(payload)
	builder.WriteByte('\n')
	combined := pruneCronLogContent(builder.String(), maxBytes, keepLines)
	return backend.Write(ctx, path, []byte(combined))
}

func pruneCronLogContent(content string, maxBytes int64, keepLines int) string {
	if maxBytes <= 0 || keepLines <= 0 {
		return content
	}
	if int64(len(content)) <= maxBytes {
		return content
	}
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lines = append(lines, trimmed)
	}
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > keepLines {
		lines = lines[len(lines)-keepLines:]
	}
	return strings.Join(lines, "\n") + "\n"
}

// ParseCronRunLogEntries parses recent entries from a jsonl log payload.
func ParseCronRunLogEntries(raw string, limit int, jobID string) []CronRunLogEntry {
	if limit <= 0 {
		limit = 200
	}
	if limit > 5000 {
		limit = 5000
	}
	if strings.TrimSpace(raw) == "" {
		return []CronRunLogEntry{}
	}
	lines := strings.Split(raw, "\n")
	var entries []CronRunLogEntry
	for i := len(lines) - 1; i >= 0 && len(entries) < limit; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var entry CronRunLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Action != "finished" || entry.JobID == "" || entry.TS == 0 {
			continue
		}
		if jobID != "" && entry.JobID != jobID {
			continue
		}
		entries = append(entries, entry)
	}
	slices.Reverse(entries)
	return entries
}

// ReadCronRunLogEntries reads recent entries from a jsonl log.
func ReadCronRunLogEntries(ctx context.Context, backend StoreBackend, path string, limit int, jobID string) ([]CronRunLogEntry, error) {
	if backend == nil {
		return []CronRunLogEntry{}, errors.New("cron store backend not configured")
	}
	data, found, err := backend.Read(ctx, path)
	if err != nil || !found {
		return []CronRunLogEntry{}, nil
	}
	return ParseCronRunLogEntries(string(data), limit, jobID), nil
}
