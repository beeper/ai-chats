package connector

import (
	"net/http"
	"strings"
)

func detectAudioMime(data []byte, fallback string) string {
	if len(data) == 0 {
		return fallback
	}

	mimeType := http.DetectContentType(data)
	if strings.HasPrefix(mimeType, "audio/") {
		return mimeType
	}
	switch mimeType {
	case "video/mp4", "application/mp4":
		return "audio/mp4"
	default:
		return fallback
	}
}
