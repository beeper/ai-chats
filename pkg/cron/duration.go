package cron

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

//lint:ignore U1000 reserved for upcoming cron duration parsing
var durationRe = regexp.MustCompile(`^(\\d+(?:\\.\\d+)?)(ms|s|m|h|d)?$`)

// parseDurationMs parses a duration string into milliseconds.
// Supports ms, s, m, h, d units. Defaults to defaultUnit when missing.
//
//lint:ignore U1000 reserved for upcoming cron duration parsing
func parseDurationMs(raw string, defaultUnit string) (int64, error) {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return 0, fmt.Errorf("invalid duration (empty)")
	}
	matches := durationRe.FindStringSubmatch(trimmed)
	if matches == nil {
		return 0, fmt.Errorf("invalid duration: %s", raw)
	}
	value := 0.0
	if _, err := fmt.Sscanf(matches[1], "%f", &value); err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, fmt.Errorf("invalid duration: %s", raw)
	}
	unit := matches[2]
	if unit == "" {
		unit = defaultUnit
	}
	switch unit {
	case "ms":
		// no-op
	case "s":
		value = value * 1000
	case "m":
		value = value * 60_000
	case "h":
		value = value * 3_600_000
	case "d":
		value = value * 86_400_000
	default:
		return 0, fmt.Errorf("invalid duration: %s", raw)
	}
	ms := math.Round(value)
	if math.IsNaN(ms) || math.IsInf(ms, 0) {
		return 0, fmt.Errorf("invalid duration: %s", raw)
	}
	return int64(ms), nil
}
