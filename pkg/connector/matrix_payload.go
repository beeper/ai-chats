package connector

import (
	"strconv"
	"strings"
)

var pollStartTypes = map[string]struct{}{
	"m.poll.start":                 {},
	"org.matrix.msc3381.poll.start": {},
}

func isPollStartEventType(eventType string) bool {
	_, ok := pollStartTypes[eventType]
	return ok
}

type pollSummary struct {
	Question string
	Answers  []string
}

func parsePollStartContent(raw map[string]any) *pollSummary {
	if raw == nil {
		return nil
	}
	payload, ok := raw["m.poll.start"].(map[string]any)
	if !ok {
		payload, ok = raw["org.matrix.msc3381.poll.start"].(map[string]any)
	}
	if !ok {
		if legacy, ok := raw["m.poll"].(map[string]any); ok {
			payload = legacy
		} else {
			return nil
		}
	}
	question := extractMatrixText(payload["question"])
	if strings.TrimSpace(question) == "" {
		return nil
	}
	answersRaw, _ := payload["answers"].([]any)
	answers := make([]string, 0, len(answersRaw))
	for _, entry := range answersRaw {
		if item, ok := entry.(map[string]any); ok {
			text := extractMatrixText(item)
			if strings.TrimSpace(text) != "" {
				answers = append(answers, text)
			}
		}
	}
	return &pollSummary{Question: question, Answers: answers}
}

func formatPollAsText(summary *pollSummary) string {
	if summary == nil {
		return ""
	}
	lines := []string{"[Poll]", summary.Question, ""}
	for i, answer := range summary.Answers {
		lines = append(lines, strconv.Itoa(i+1)+". "+answer)
	}
	return strings.Join(lines, "\n")
}

func extractMatrixText(raw any) string {
	if rawMap, ok := raw.(map[string]any); ok {
		if v, ok := rawMap["m.text"].(string); ok {
			return v
		}
		if v, ok := rawMap["org.matrix.msc1767.text"].(string); ok {
			return v
		}
		if v, ok := rawMap["body"].(string); ok {
			return v
		}
	}
	return ""
}

type matrixLocationPayload struct {
	Text string
}

func resolveMatrixLocation(raw map[string]any) *matrixLocationPayload {
	if raw == nil {
		return nil
	}
	geoRaw, _ := raw["geo_uri"].(string)
	geoRaw = strings.TrimSpace(geoRaw)
	if geoRaw == "" {
		return nil
	}
	location, ok := parseGeoURI(geoRaw)
	if !ok {
		return nil
	}
	caption := ""
	if body, ok := raw["body"].(string); ok {
		caption = strings.TrimSpace(body)
	}
	normalized := NormalizedLocation{
		Latitude:  location.Latitude,
		Longitude: location.Longitude,
		Accuracy:  location.Accuracy,
		Caption:   caption,
		Source:    "pin",
		IsLive:    false,
	}
	return &matrixLocationPayload{Text: formatLocationText(normalized)}
}

type geoURI struct {
	Latitude  float64
	Longitude float64
	Accuracy  *float64
}

func parseGeoURI(value string) (geoURI, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return geoURI{}, false
	}
	if !strings.HasPrefix(strings.ToLower(trimmed), "geo:") {
		return geoURI{}, false
	}
	payload := strings.TrimPrefix(trimmed, "geo:")
	parts := strings.Split(payload, ";")
	coords := strings.Split(parts[0], ",")
	if len(coords) < 2 {
		return geoURI{}, false
	}
	lat, err1 := strconv.ParseFloat(coords[0], 64)
	lon, err2 := strconv.ParseFloat(coords[1], 64)
	if err1 != nil || err2 != nil {
		return geoURI{}, false
	}
	var accuracy *float64
	for _, part := range parts[1:] {
		segment := strings.TrimSpace(part)
		if segment == "" {
			continue
		}
		kv := strings.SplitN(segment, "=", 2)
		key := strings.ToLower(strings.TrimSpace(kv[0]))
		val := ""
		if len(kv) > 1 {
			val = strings.TrimSpace(kv[1])
		}
		if key == "u" {
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				accuracy = &v
			}
		}
	}
	return geoURI{Latitude: lat, Longitude: lon, Accuracy: accuracy}, true
}
