package connector

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
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

func hasSenderPrefix(body string, senderLabel string) bool {
	label := strings.TrimSpace(senderLabel)
	if label == "" || strings.TrimSpace(body) == "" {
		return false
	}
	return getSenderPrefixRE(label).MatchString(body)
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

func formatLocationText(location NormalizedLocation) string {
	resolved := resolveLocation(location)
	coords := fmt.Sprintf("%.6f, %.6f", resolved.Latitude, resolved.Longitude)
	accuracy := ""
	if resolved.Accuracy != nil {
		accuracy = fmt.Sprintf(" ±%dm", int(*resolved.Accuracy+0.5))
	}
	var header string
	if resolved.Source == "live" || resolved.IsLive {
		header = "Live location: " + coords + accuracy
	} else if resolved.Name != "" || resolved.Address != "" {
		label := strings.TrimSpace(strings.Join([]string{strings.TrimSpace(resolved.Name), strings.TrimSpace(resolved.Address)}, " - "))
		label = strings.Trim(label, " -")
		header = "Location: " + label + " (" + coords + accuracy + ")"
	} else {
		header = "Location: " + coords + accuracy
	}
	caption := strings.TrimSpace(resolved.Caption)
	if caption == "" {
		return header
	}
	return header + "\n" + caption
}
