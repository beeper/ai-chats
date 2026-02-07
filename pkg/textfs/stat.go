package textfs

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	StatTypeFile = "file"
	StatTypeDir  = "dir"
)

type StatEntry struct {
	Path      string
	Type      string
	Size      int
	Hash      string
	Source    string
	UpdatedAt int64
	Entries   int
}

func (s *Store) Stat(ctx context.Context, relPath string) (*StatEntry, error) {
	trimmed := strings.TrimSpace(relPath)
	if trimmed == "" {
		return nil, errors.New("path is required")
	}
	forceDir := strings.HasSuffix(trimmed, "/") || strings.HasSuffix(trimmed, "\\")

	if !forceDir {
		entry, found, err := s.Read(ctx, trimmed)
		if err != nil {
			return nil, err
		}
		if found {
			return &StatEntry{
				Path:      entry.Path,
				Type:      StatTypeFile,
				Size:      len(entry.Content),
				Hash:      entry.Hash,
				Source:    entry.Source,
				UpdatedAt: entry.UpdatedAt,
			}, nil
		}
	}

	dir, err := NormalizeDir(trimmed)
	if err != nil {
		return nil, err
	}
	entries, err := s.ListWithPrefix(ctx, dir)
	if err != nil {
		return nil, err
	}
	if dir != "" {
		found := false
		for _, entry := range entries {
			if entry.Path == dir || strings.HasPrefix(entry.Path, dir+"/") {
				found = true
				break
			}
		}
		if !found && IsVirtualDir(dir) {
			return &StatEntry{
				Path:    dir,
				Type:    StatTypeDir,
				Entries: 0,
			}, nil
		}
		if !found {
			return nil, fmt.Errorf("path not found: %s", relPath)
		}
	}
	names, _ := s.DirEntries(entries, dir)
	return &StatEntry{
		Path:    dir,
		Type:    StatTypeDir,
		Entries: len(names),
	}, nil
}
