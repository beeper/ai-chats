package media

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/beeper/ai-chats/pkg/shared/stringutil"
)

// DownloadURL fetches a file from rawURL over HTTP(S) and returns the bytes,
// resolved MIME type, and any error. When maxBytes > 0 the download is
// rejected if the server-advertised Content-Length or the actual body exceeds
// that limit. fallbackMime is used when the server does not return a usable
// Content-Type header.
func DownloadURL(ctx context.Context, rawURL, fallbackMime string, maxBytes int64) ([]byte, string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, "", fmt.Errorf("missing download URL")
	}

	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("file download failed with status %d", resp.StatusCode)
	}
	if maxBytes > 0 && resp.ContentLength > 0 && resp.ContentLength > maxBytes {
		return nil, "", fmt.Errorf("file too large: %d bytes (max %d)", resp.ContentLength, maxBytes)
	}

	var reader io.Reader = resp.Body
	if maxBytes > 0 {
		reader = io.LimitReader(resp.Body, maxBytes+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", err
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return nil, "", fmt.Errorf("file too large: %d bytes (max %d)", int64(len(data)), maxBytes)
	}

	mimeType := stringutil.NormalizeMimeType(resp.Header.Get("Content-Type"))
	if mimeType == "" {
		mimeType = stringutil.NormalizeMimeType(fallbackMime)
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return data, mimeType, nil
}
