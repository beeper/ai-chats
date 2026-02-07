package agents

import (
	"errors"
	"math"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const DefaultSoulEvilFilename = "SOUL_EVIL.md"

// SoulEvilConfig controls persona swapping behavior.
type SoulEvilConfig struct {
	File   string         `yaml:"file" json:"file,omitempty"`
	Chance float64        `yaml:"chance" json:"chance,omitempty"`
	Purge  *SoulEvilPurge `yaml:"purge" json:"purge,omitempty"`
}

// SoulEvilPurge defines a daily purge window.
type SoulEvilPurge struct {
	At       string `yaml:"at" json:"at,omitempty"`
	Duration string `yaml:"duration" json:"duration,omitempty"`
}

type SoulEvilDecision struct {
	UseEvil  bool
	Reason   string
	FileName string
}

type SoulEvilCheckParams struct {
	Config       *SoulEvilConfig
	UserTimezone string
	Now          time.Time
	Random       func() float64
}

func clampChance(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func resolveTimezone(raw string) *time.Location {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.UTC
	}
	if strings.EqualFold(trimmed, "utc") {
		return time.UTC
	}
	loc, err := time.LoadLocation(trimmed)
	if err != nil {
		return time.UTC
	}
	return loc
}

var purgeAtRegex = regexp.MustCompile(`^([01]?\d|2[0-3]):([0-5]\d)$`)

func parsePurgeAt(raw string) (int, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, false
	}
	match := purgeAtRegex.FindStringSubmatch(trimmed)
	if len(match) != 3 {
		return 0, false
	}
	hour, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	minute, err := strconv.Atoi(match[2])
	if err != nil {
		return 0, false
	}
	return hour*60 + minute, true
}

func parseDurationWithDefaultUnit(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, errors.New("empty duration")
	}
	if _, err := strconv.Atoi(trimmed); err == nil {
		trimmed = trimmed + "m"
	}
	return time.ParseDuration(trimmed)
}

func timeOfDayMs(now time.Time, loc *time.Location) int64 {
	local := now.In(loc)
	hour, min, sec := local.Clock()
	ms := int64(hour*3600+min*60+sec)*1000 + int64(local.Nanosecond()/1e6)
	return ms
}

func isWithinDailyPurgeWindow(at string, duration string, now time.Time, loc *time.Location) bool {
	startMinutes, ok := parsePurgeAt(at)
	if !ok {
		return false
	}
	durationValue, err := parseDurationWithDefaultUnit(duration)
	if err != nil || durationValue <= 0 {
		return false
	}
	dayMs := int64(24 * time.Hour / time.Millisecond)
	if durationValue.Milliseconds() >= dayMs {
		return true
	}
	startMs := int64(startMinutes) * 60 * 1000
	endMs := startMs + durationValue.Milliseconds()
	nowMs := timeOfDayMs(now, loc)
	if endMs < dayMs {
		return nowMs >= startMs && nowMs < endMs
	}
	wrappedEnd := endMs % dayMs
	return nowMs >= startMs || nowMs < wrappedEnd
}

// DecideSoulEvil decides whether to swap SOUL content for this run.
func DecideSoulEvil(params SoulEvilCheckParams) SoulEvilDecision {
	fileName := DefaultSoulEvilFilename
	if params.Config != nil && strings.TrimSpace(params.Config.File) != "" {
		fileName = strings.TrimSpace(params.Config.File)
	}
	if params.Config == nil {
		return SoulEvilDecision{UseEvil: false, FileName: fileName}
	}

	loc := resolveTimezone(params.UserTimezone)
	now := params.Now
	if now.IsZero() {
		now = time.Now()
	}

	if params.Config.Purge != nil {
		if isWithinDailyPurgeWindow(params.Config.Purge.At, params.Config.Purge.Duration, now, loc) {
			return SoulEvilDecision{UseEvil: true, Reason: "purge", FileName: fileName}
		}
	}

	chance := clampChance(params.Config.Chance)
	if chance > 0 {
		rnd := params.Random
		if rnd == nil {
			rnd = func() float64 { return randFloat64() }
		}
		if rnd() < chance {
			return SoulEvilDecision{UseEvil: true, Reason: "chance", FileName: fileName}
		}
	}

	return SoulEvilDecision{UseEvil: false, FileName: fileName}
}

func randFloat64() float64 {
	return rand.Float64()
}
