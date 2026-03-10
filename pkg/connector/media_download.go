package connector

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/shared/media"
)

func (oc *AIClient) downloadMediaBytes(
	ctx context.Context,
	mediaURL string,
	encryptedFile *event.EncryptedFileInfo,
	maxBytes int,
	fallbackMime string,
) ([]byte, string, error) {
	downloadURL := mediaURL
	if encryptedFile != nil {
		downloadURL = string(encryptedFile.URL)
	}

	if strings.HasPrefix(downloadURL, "file://") || strings.HasPrefix(downloadURL, "/") {
		path := downloadURL
		if strings.HasPrefix(path, "file://") {
			path = strings.TrimPrefix(path, "file://")
			if unescaped, err := url.PathUnescape(path); err == nil {
				path = unescaped
			}
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, "", fmt.Errorf("failed to stat local file: %w", err)
		}
		if maxBytes > 0 && info.Size() > int64(maxBytes) {
			return nil, "", fmt.Errorf("media too large: %d bytes (max %d)", info.Size(), maxBytes)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read local file: %w", err)
		}
		if encryptedFile != nil {
			if err := encryptedFile.DecryptInPlace(data); err != nil {
				return nil, "", fmt.Errorf("failed to decrypt media: %w", err)
			}
		}
		mimeType := mime.TypeByExtension(filepath.Ext(path))
		if mimeType == "" {
			mimeType = http.DetectContentType(data)
		}
		mimeType = normalizeFallbackMime(mimeType, fallbackMime)
		return data, mimeType, nil
	}

	if strings.HasPrefix(downloadURL, "mxc://") {
		if oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.Bot == nil {
			return nil, "", errors.New("matrix API unavailable for MXC media download")
		}
		data, err := oc.UserLogin.Bridge.Bot.DownloadMedia(ctx, id.ContentURIString(downloadURL), encryptedFile)
		if err != nil {
			return nil, "", fmt.Errorf("failed to download media via Matrix API: %w", err)
		}
		if maxBytes > 0 && len(data) > maxBytes {
			return nil, "", fmt.Errorf("media too large (max %d bytes)", maxBytes)
		}
		mimeType := normalizeFallbackMime(http.DetectContentType(data), fallbackMime)
		return data, mimeType, nil
	}

	data, mimeType, err := media.DownloadURL(ctx, downloadURL, fallbackMime, int64(maxBytes))
	if err != nil {
		return nil, "", err
	}
	mimeType = normalizeFallbackMime(mimeType, fallbackMime)
	return data, mimeType, nil
}

func normalizeFallbackMime(actual string, fallback string) string {
	actual = strings.TrimSpace(actual)
	if actual == "" || actual == "application/octet-stream" {
		actual = strings.TrimSpace(fallback)
	}
	if actual == "" {
		actual = "application/octet-stream"
	}
	return actual
}
