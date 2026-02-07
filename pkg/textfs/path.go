package textfs

import (
	"errors"
	"path"
	"strings"
)

// NormalizePath normalizes a virtual file path and prevents escaping the root.
func NormalizePath(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("path is required")
	}
	cleaned := strings.ReplaceAll(trimmed, "\\", "/")
	cleaned = strings.TrimPrefix(cleaned, "file://")
	cleaned = strings.TrimLeft(cleaned, "/")
	cleaned = strings.TrimPrefix(cleaned, "./")
	cleaned = path.Clean(cleaned)
	if cleaned == "." || cleaned == "" {
		return "", errors.New("path is required")
	}
	if strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, "/..") {
		return "", errors.New("path escapes virtual root")
	}
	cleaned = strings.TrimSuffix(cleaned, "/")
	return cleaned, nil
}

// NormalizeDir normalizes a directory path; empty means root.
func NormalizeDir(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "." || trimmed == "/" {
		return "", nil
	}
	cleaned, err := NormalizePath(trimmed)
	if err != nil {
		return "", err
	}
	return cleaned, nil
}

// IsMemoryPath returns true for MEMORY.md or memory/*.md.
func IsMemoryPath(relPath string) bool {
	normalized := strings.TrimSpace(relPath)
	if normalized == "" {
		return false
	}
	normalized = strings.ReplaceAll(normalized, "\\", "/")
	normalized = strings.TrimPrefix(normalized, "./")
	normalized = strings.TrimLeft(normalized, "/")
	if normalized == "MEMORY.md" || normalized == "memory.md" {
		return true
	}
	return strings.HasPrefix(normalized, "memory/")
}

// IsVirtualDir returns true for virtual directories that should exist even when empty.
func IsVirtualDir(relDir string) bool {
	normalized, err := NormalizeDir(relDir)
	if err != nil || normalized == "" {
		return false
	}
	return normalized == "memory"
}

// VirtualRootEntries returns the virtual root directories.
func VirtualRootEntries() []string {
	return []string{"memory/"}
}

// ClassifySource returns the default source label for a path.
func ClassifySource(path string) string {
	if IsMemoryPath(path) {
		return "memory"
	}
	return "workspace"
}
