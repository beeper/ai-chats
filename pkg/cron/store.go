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
	if override == "" {
		override = strings.TrimSpace(os.Getenv("CLAWDBOT_STATE_DIR"))
	}
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
// On parse failure, it attempts to load from the .bak file.
func LoadCronStore(ctx context.Context, backend StoreBackend, storePath string) (CronStoreFile, error) {
	if backend == nil {
		return CronStoreFile{Version: 1, Jobs: []CronJob{}}, errors.New("cron store backend not configured")
	}
	data, found, err := backend.Read(ctx, storePath)
	if err != nil {
		return CronStoreFile{Version: 1, Jobs: []CronJob{}}, fmt.Errorf("cron store read: %w", err)
	}
	if !found {
		return CronStoreFile{Version: 1, Jobs: []CronJob{}}, nil
	}
	if parsed, parseErr := parseCronStoreData(data); parseErr == nil {
		return parsed, nil
	}
	// Main file corrupt — try .bak
	bakData, bakFound, bakErr := backend.Read(ctx, storePath+".bak")
	if bakErr != nil || !bakFound {
		return CronStoreFile{Version: 1, Jobs: []CronJob{}}, fmt.Errorf("cron store corrupt and no valid backup: %s", storePath)
	}
	if parsed, parseErr := parseCronStoreData(bakData); parseErr == nil {
		return parsed, nil
	}
	return CronStoreFile{Version: 1, Jobs: []CronJob{}}, fmt.Errorf("cron store and backup both corrupt: %s", storePath)
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

// SaveCronStore writes the JSON store and keeps a .bak copy in virtual storage.
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
	if err := backend.Write(ctx, storePath, payload); err != nil {
		return err
	}
	_ = backend.Write(ctx, storePath+".bak", payload)
	return nil
}
