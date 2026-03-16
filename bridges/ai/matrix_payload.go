package ai

import (
	"strconv"
	"strings"
)

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
	latRaw, lonRaw, ok := strings.Cut(parts[0], ",")
	if !ok {
		return geoURI{}, false
	}
	lat, err1 := strconv.ParseFloat(latRaw, 64)
	lon, err2 := strconv.ParseFloat(lonRaw, 64)
	if err1 != nil || err2 != nil {
		return geoURI{}, false
	}
	var accuracy *float64
	for _, part := range parts[1:] {
		segment := strings.TrimSpace(part)
		if segment == "" {
			continue
		}
		keyPart, valPart, _ := strings.Cut(segment, "=")
		key := strings.ToLower(strings.TrimSpace(keyPart))
		val := strings.TrimSpace(valPart)
		if key == "u" {
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				accuracy = &v
			}
		}
	}
	return geoURI{Latitude: lat, Longitude: lon, Accuracy: accuracy}, true
}
