package connector

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

var senderPrefixRECache sync.Map

func getSenderPrefixRE(label string) *regexp.Regexp {
	if cached, ok := senderPrefixRECache.Load(label); ok {
		return cached.(*regexp.Regexp)
	}
	re := regexp.MustCompile(`(?i)(^|\n|\]\s*)` + regexp.QuoteMeta(label) + `:\s`)
	senderPrefixRECache.Store(label, re)
	return re
}

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
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func formatZonedTimestamp(t time.Time, loc *time.Location) string {
	if loc != nil {
		t = t.In(loc)
	}
	return t.Format("2006-01-02 15:04:05.000 MST")
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

// envelopePrefixRE matches the [Channel ...] envelope prefix added by formatAgentEnvelope.
// This covers known channel names used across platforms.
var envelopePrefixRE = regexp.MustCompile(
	`^\[(?:Desktop|Desktop API|WebChat|WhatsApp|Telegram|Signal|Slack|Discord|iMessage|Matrix|Teams|SMS|Google Chat|Zalo|BlueBubbles|Channel)[\s\S]*?\]\s*`,
)

// StripEnvelope removes the [Channel Timestamp] envelope prefix from a message body.
// This is useful when replaying historical messages to the model — the envelope is
// informative for the current turn but noisy in history.
func StripEnvelope(text string) string {
	return envelopePrefixRE.ReplaceAllString(text, "")
}

var senderMetaLineRE = regexp.MustCompile(`(?i)(^|\n)\[from:\s*[^\]]+\]`)

func hasSenderPrefix(body string, senderLabel string) bool {
	label := strings.TrimSpace(senderLabel)
	if label == "" || strings.TrimSpace(body) == "" {
		return false
	}
	return getSenderPrefixRE(label).MatchString(body)
}

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
	if hasSenderPrefix(body, label) {
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
