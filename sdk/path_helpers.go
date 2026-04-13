package sdk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ExpandUserHome(path string) (string, error) {
	rest, isTilde := strings.CutPrefix(strings.TrimSpace(path), "~")
	if !isTilde {
		return strings.TrimSpace(path), nil
	}
	if rest != "" && rest[0] != '/' {
		return strings.TrimSpace(path), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, rest), nil
}

func NormalizeAbsolutePath(path string) (string, error) {
	expanded, err := ExpandUserHome(path)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expanded) {
		return "", fmt.Errorf("path must be absolute")
	}
	return filepath.Clean(expanded), nil
}
