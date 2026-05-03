package ai

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const defaultTimezone = "UTC"

func normalizeTimezone(raw string) (string, *time.Location, error) {
	tz := strings.TrimSpace(raw)
	if tz == "" {
		return "", nil, errors.New("empty timezone")
	}
	if strings.EqualFold(tz, "utc") {
		tz = "UTC"
	}
	if strings.EqualFold(tz, "local") {
		return "", nil, fmt.Errorf("timezone must be an IANA name, not %q", tz)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return "", nil, err
	}
	return loc.String(), loc, nil
}

func (oc *AIClient) resolveUserTimezone() (string, *time.Location) {
	if oc == nil || oc.UserLogin == nil {
		return defaultTimezone, time.UTC
	}
	loginCfg := oc.loginConfigSnapshot(context.Background())
	if loginCfg != nil && strings.TrimSpace(loginCfg.Timezone) != "" {
		if tz, loc, err := normalizeTimezone(loginCfg.Timezone); err == nil {
			return tz, loc
		}
	}
	if tz := os.Getenv("TZ"); tz != "" {
		if tz, loc, err := normalizeTimezone(tz); err == nil {
			return tz, loc
		}
	}
	return defaultTimezone, time.UTC
}
