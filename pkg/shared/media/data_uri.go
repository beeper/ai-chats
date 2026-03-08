package media

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ParseDataURI parses a base64 data URI and returns raw base64 data and mime type.
func ParseDataURI(dataURI string) (string, string, error) {
	// Format: data:[<mediatype>][;base64],<data>
	if !strings.HasPrefix(dataURI, "data:") {
		return "", "", errors.New("not a data URI")
	}

	rest := dataURI[5:]
	metadata, data, ok := strings.Cut(rest, ",")
	if !ok {
		return "", "", errors.New("invalid data URI: no comma separator")
	}

	if !strings.Contains(metadata, ";base64") {
		return "", "", errors.New("only base64 data URIs are supported")
	}

	mimeType := strings.Split(metadata, ";")[0]
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	return data, mimeType, nil
}

// DecodeBase64 decodes raw/base64 data or data URIs and returns bytes plus mime type.
func DecodeBase64(b64Data string) ([]byte, string, error) {
	var mimeType string

	// Strip data-URI prefix if present, extracting the mime type.
	if strings.HasPrefix(b64Data, "data:") {
		rawB64, parsedMime, err := ParseDataURI(b64Data)
		if err != nil {
			return nil, "", fmt.Errorf("parsing data URI: %w", err)
		}
		b64Data = rawB64
		mimeType = parsedMime
	}

	data, err := base64.StdEncoding.DecodeString(b64Data)
	if err != nil {
		data, err = base64.URLEncoding.DecodeString(b64Data)
		if err != nil {
			return nil, "", fmt.Errorf("base64 decode failed: %w", err)
		}
	}

	if mimeType == "" {
		mimeType = http.DetectContentType(data)
		if mimeType == "application/octet-stream" {
			mimeType = "image/png"
		}
	}

	return data, mimeType, nil
}
