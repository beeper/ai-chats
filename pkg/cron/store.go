package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	json5 "github.com/yosuke-furukawa/json5/encoding/json5"

	"github.com/beeper/ai-bridge/pkg/textfs"
)

var emptyCronStore = CronStoreFile{Version: 1, Jobs: []CronJob{}}

const (
	defaultCronDir       = "cron"
	defaultCronFileName  = "jobs.json"
	defaultCronStorePath = defaultCronDir + "/" + defaultCronFileName
)

// ResolveCronStorePath resolves the virtual JSON store path.
func ResolveCronStorePath(storePath string) string {
	trimmed := strings.TrimSpace(storePath)
	if trimmed != "" {
		if normalized, err := textfs.NormalizePath(trimmed); err == nil {
			return normalized
		}
	}
	override := strings.TrimSpace(os.Getenv("OPENCLAW_STATE_DIR"))
	if override != "" {
		if dir, err := textfs.NormalizeDir(override); err == nil {
			if dir == "" {
				return defaultCronStorePath
			}
			return path.Join(dir, defaultCronDir, defaultCronFileName)
		}
	}
	return defaultCronStorePath
}

// LoadCronStore reads the JSON store, tolerating missing files.
func LoadCronStore(ctx context.Context, backend StoreBackend, storePath string) (CronStoreFile, error) {
	if backend == nil {
		return emptyCronStore, errors.New("cron store backend not configured")
	}
	data, found, err := backend.Read(ctx, storePath)
	if err != nil {
		return emptyCronStore, fmt.Errorf("cron store read: %w", err)
	}
	if !found {
		return emptyCronStore, nil
	}
	parsed, parseErr := parseCronStoreData(data)
	if parseErr != nil {
		return emptyCronStore, fmt.Errorf("cron store corrupt: %s: %w", storePath, parseErr)
	}
	return parsed, nil
}

func parseCronStoreData(data []byte) (CronStoreFile, error) {
	var raw map[string]any
	if err := json5.Unmarshal(data, &raw); err != nil {
		return CronStoreFile{}, fmt.Errorf("json5 parse: %w", err)
	}
	if raw == nil {
		raw = map[string]any{}
	}
	if jobsRaw, ok := raw["jobs"].([]any); ok {
		normalizedJobs := make([]any, 0, len(jobsRaw))
		for _, rawJob := range jobsRaw {
			normalized := normalizeCronJobInputRaw(rawJob, true)
			if normalized == nil {
				continue
			}
			normalizedJobs = append(normalizedJobs, normalized)
		}
		raw["jobs"] = normalizedJobs
	}
	if _, ok := raw["version"]; !ok {
		raw["version"] = float64(1)
	}
	normalizedData, err := json.Marshal(raw)
	if err != nil {
		return CronStoreFile{}, fmt.Errorf("json marshal: %w", err)
	}
	var parsed CronStoreFile
	if err := json.Unmarshal(normalizedData, &parsed); err != nil {
		return CronStoreFile{}, fmt.Errorf("json unmarshal: %w", err)
	}
	if parsed.Version == 0 {
		parsed.Version = 1
	}
	if parsed.Jobs == nil {
		parsed.Jobs = []CronJob{}
	}
	return parsed, nil
}

// SaveCronStore writes the JSON store.
func SaveCronStore(ctx context.Context, backend StoreBackend, storePath string, store CronStoreFile) error {
	if backend == nil {
		return errors.New("cron store backend not configured")
	}
	if store.Version == 0 {
		store.Version = 1
	}
	payload, err := json5.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return backend.Write(ctx, storePath, payload)
}
