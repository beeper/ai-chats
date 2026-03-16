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
	return cleaned, nil
}

// IsMemoryPath returns true for MEMORY.md or memory/*.md.
func IsMemoryPath(relPath string) bool {
	normalized, err := NormalizePath(relPath)
	if err != nil {
		return false
	}
	if normalized == "MEMORY.md" || normalized == "memory.md" {
		return true
	}
	return strings.HasPrefix(normalized, "memory/")
}

// ClassifySource returns the default source label for a path.
func ClassifySource(path string) string {
	if IsMemoryPath(path) {
		return "memory"
	}
	return "workspace"
}
