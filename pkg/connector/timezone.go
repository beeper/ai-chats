package connector

import (
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
	if oc.connector != nil && oc.connector.Config.Agents != nil && oc.connector.Config.Agents.Defaults != nil {
		cfgTZ := strings.TrimSpace(oc.connector.Config.Agents.Defaults.UserTimezone)
		if cfgTZ != "" {
			if tz, loc, err := normalizeTimezone(cfgTZ); err == nil {
				return tz, loc
			}
		}
	}
	loginMeta := loginMetadata(oc.UserLogin)
	if loginMeta != nil && strings.TrimSpace(loginMeta.Timezone) != "" {
		if tz, loc, err := normalizeTimezone(loginMeta.Timezone); err == nil {
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
