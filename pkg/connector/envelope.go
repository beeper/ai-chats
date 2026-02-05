package connector

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

type EnvelopeFormatOptions struct {
	Timezone         string
	IncludeTimestamp bool
	IncludeElapsed   bool
	UserTimezone     string
}

type envelopeTimezone struct {
	mode     string
	location *time.Location
}

func (oc *AIClient) resolveEnvelopeFormatOptions() EnvelopeFormatOptions {
	opts := EnvelopeFormatOptions{
		Timezone:         "local",
		IncludeTimestamp: true,
		IncludeElapsed:   true,
	}
	if oc == nil || oc.connector == nil {
		return opts
	}
	if oc.connector.Config.Agents != nil && oc.connector.Config.Agents.Defaults != nil {
		def := oc.connector.Config.Agents.Defaults
		if strings.TrimSpace(def.EnvelopeTimezone) != "" {
			opts.Timezone = strings.TrimSpace(def.EnvelopeTimezone)
		}
		if strings.EqualFold(strings.TrimSpace(def.EnvelopeTimestamp), "off") {
			opts.IncludeTimestamp = false
		}
		if strings.EqualFold(strings.TrimSpace(def.EnvelopeElapsed), "off") {
			opts.IncludeElapsed = false
		}
		if strings.TrimSpace(def.UserTimezone) != "" {
			opts.UserTimezone = strings.TrimSpace(def.UserTimezone)
		}
	}
	if strings.TrimSpace(opts.UserTimezone) == "" {
		if tz, _ := oc.resolveUserTimezone(); tz != "" {
			opts.UserTimezone = tz
		}
	}
	return opts
}

func resolveEnvelopeTimezone(opts EnvelopeFormatOptions) envelopeTimezone {
	trimmed := strings.TrimSpace(opts.Timezone)
	if trimmed == "" {
		return envelopeTimezone{mode: "local"}
	}
	lowered := strings.ToLower(trimmed)
	switch lowered {
	case "utc", "gmt":
		return envelopeTimezone{mode: "utc"}
	case "local", "host":
		return envelopeTimezone{mode: "local"}
	case "user":
		if strings.TrimSpace(opts.UserTimezone) != "" {
			if loc, err := time.LoadLocation(opts.UserTimezone); err == nil {
				return envelopeTimezone{mode: "iana", location: loc}
			}
		}
		return envelopeTimezone{mode: "utc"}
	}
	if loc, err := time.LoadLocation(trimmed); err == nil {
		return envelopeTimezone{mode: "iana", location: loc}
	}
	return envelopeTimezone{mode: "utc"}
}

func formatUtcTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04Z")
}

func formatZonedTimestamp(t time.Time, loc *time.Location) string {
	if loc != nil {
		t = t.In(loc)
	}
	return t.Format("2006-01-02 15:04 MST")
}

func formatElapsedTime(current, previous time.Time) string {
	elapsed := current.Sub(previous)
	if elapsed < 0 {
		return ""
	}
	seconds := int(elapsed.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	if hours < 24 {
		return fmt.Sprintf("%dh", hours)
	}
	days := hours / 24
	return fmt.Sprintf("%dd", days)
}

func formatAgentEnvelope(params struct {
	Channel         string
	From            string
	Body            string
	Timestamp       time.Time
	HasTimestamp    bool
	PreviousTime    time.Time
	HasPreviousTime bool
	Envelope        EnvelopeFormatOptions
}) string {
	channel := strings.TrimSpace(params.Channel)
	if channel == "" {
		channel = "Channel"
	}
	parts := []string{channel}
	elapsed := ""
	if params.Envelope.IncludeElapsed && params.HasTimestamp && params.HasPreviousTime {
		elapsed = formatElapsedTime(params.Timestamp, params.PreviousTime)
	}
	from := strings.TrimSpace(params.From)
	if from != "" {
		if elapsed != "" {
			parts = append(parts, fmt.Sprintf("%s +%s", from, elapsed))
		} else {
			parts = append(parts, from)
		}
	} else if elapsed != "" {
		parts = append(parts, fmt.Sprintf("+%s", elapsed))
	}
	if params.Envelope.IncludeTimestamp && params.HasTimestamp {
		zone := resolveEnvelopeTimezone(params.Envelope)
		switch zone.mode {
		case "utc":
			parts = append(parts, formatUtcTimestamp(params.Timestamp))
		case "iana":
			parts = append(parts, formatZonedTimestamp(params.Timestamp, zone.location))
		default:
			parts = append(parts, formatZonedTimestamp(params.Timestamp, nil))
		}
	}
	header := "[" + strings.Join(parts, " ") + "]"
	return fmt.Sprintf("%s %s", header, params.Body)
}

var senderMetaLineRE = regexp.MustCompile(`(?i)(^|\n)\[from:\s*[^\]]+\]`)

func formatInboundBodyWithSenderMeta(body string, senderLabel string, isGroup bool) string {
	if !isGroup || strings.TrimSpace(body) == "" {
		return body
	}
	if senderMetaLineRE.MatchString(body) {
		return body
	}
	label := strings.TrimSpace(senderLabel)
	if label == "" {
		return body
	}
	// Treat "Alice: hi" as already containing sender meta.
	pattern := regexp.MustCompile(`(?i)(^|\n|\]\s*)` + regexp.QuoteMeta(label) + `:\s`)
	if pattern.MatchString(body) {
		return body
	}
	return body + "\n[from: " + label + "]"
}

func formatLocationText(location NormalizedLocation) string {
	resolved := resolveLocation(location)
	coords := fmt.Sprintf("%.6f, %.6f", resolved.Latitude, resolved.Longitude)
	accuracy := ""
	if resolved.Accuracy != nil {
		accuracy = fmt.Sprintf(" \u00b1%dm", int(*resolved.Accuracy+0.5))
	}
	var header string
	if resolved.Source == "live" || resolved.IsLive {
		header = "🛰 Live location: " + coords + accuracy
	} else if resolved.Name != "" || resolved.Address != "" {
		label := strings.TrimSpace(strings.Join([]string{strings.TrimSpace(resolved.Name), strings.TrimSpace(resolved.Address)}, " — "))
		label = strings.Trim(label, " —")
		header = "📍 " + label + " (" + coords + accuracy + ")"
	} else {
		header = "📍 " + coords + accuracy
	}
	caption := strings.TrimSpace(resolved.Caption)
	if caption == "" {
		return header
	}
	return header + "\n" + caption
}

type NormalizedLocation struct {
	Latitude  float64
	Longitude float64
	Accuracy  *float64
	Name      string
	Address   string
	Caption   string
	Source    string // pin|place|live
	IsLive    bool
}

func resolveLocation(loc NormalizedLocation) NormalizedLocation {
	resolved := loc
	source := strings.TrimSpace(resolved.Source)
	if source == "" {
		if resolved.IsLive {
			source = "live"
		} else if resolved.Name != "" || resolved.Address != "" {
			source = "place"
		} else {
			source = "pin"
		}
	}
	resolved.Source = source
	if !resolved.IsLive && source == "live" {
		resolved.IsLive = true
	}
	return resolved
}
