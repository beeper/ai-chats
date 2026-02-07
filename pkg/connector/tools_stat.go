package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type statOutput struct {
	Path      string `json:"path"`
	Type      string `json:"type"`
	Size      int    `json:"size,omitempty"`
	Hash      string `json:"hash,omitempty"`
	Source    string `json:"source,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
	Entries   int    `json:"entries,omitempty"`
}

func executeStat(ctx context.Context, args map[string]any) (string, error) {
	store, err := textFSStore(ctx)
	if err != nil {
		return "", err
	}
	opCtx, cancel := context.WithTimeout(ctx, textFSToolTimeout)
	defer cancel()
	raw, ok := args["path"].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("missing or invalid 'path' argument")
	}
	entry, err := store.Stat(opCtx, raw)
	if err != nil {
		return "", err
	}
	output := statOutput{
		Path:      entry.Path,
		Type:      entry.Type,
		Size:      entry.Size,
		Hash:      entry.Hash,
		Source:    entry.Source,
		UpdatedAt: entry.UpdatedAt,
		Entries:   entry.Entries,
	}
	blob, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format stat result: %w", err)
	}
	return string(blob), nil
}
