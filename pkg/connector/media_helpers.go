package connector

import "strings"

func buildMediaMetadataBody(caption, suffix string, understanding *mediaUnderstandingResult) string {
	if understanding != nil && strings.TrimSpace(understanding.Body) != "" {
		return understanding.Body
	}
	return caption + suffix
}
