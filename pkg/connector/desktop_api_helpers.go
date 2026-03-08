package connector

import (
	"errors"
	"strings"
)

func parseDesktopAPIAddArgs(args []string) (name, token, baseURL string, err error) {
	if len(args) == 0 {
		return "", "", "", errors.New("missing args")
	}

	trimmed := make([]string, 0, len(args))
	for _, raw := range args {
		part := strings.TrimSpace(raw)
		if part != "" {
			trimmed = append(trimmed, part)
		}
	}
	if len(trimmed) == 0 {
		return "", "", "", errors.New("missing args")
	}

	if len(trimmed) == 1 {
		return "", trimmed[0], "", nil
	}

	if len(trimmed) == 2 {
		if isLikelyHTTPURL(trimmed[1]) {
			return "", trimmed[0], trimmed[1], nil
		}
		return normalizeDesktopInstanceName(trimmed[0]), trimmed[1], "", nil
	}

	return normalizeDesktopInstanceName(trimmed[0]), trimmed[1], strings.TrimSpace(strings.Join(trimmed[2:], " ")), nil
}
