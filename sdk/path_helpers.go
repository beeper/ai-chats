package sdk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func NormalizeAbsolutePath(path string) (string, error) {
	expanded := strings.TrimSpace(path)
	if rest, isTilde := strings.CutPrefix(expanded, "~"); isTilde {
		if rest == "" || rest[0] == '/' {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			expanded = filepath.Join(home, rest)
		}
	}
	if !filepath.IsAbs(expanded) {
		return "", fmt.Errorf("path must be absolute")
	}
	return filepath.Clean(expanded), nil
}
