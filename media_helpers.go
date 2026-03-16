package agentremote

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// DownloadMediaBytes downloads media from a Matrix content URI and returns the raw bytes and detected MIME type.
func DownloadMediaBytes(ctx context.Context, login *bridgev2.UserLogin, mediaURL string, encFile *event.EncryptedFileInfo, maxBytes int64) ([]byte, string, error) {
	if strings.TrimSpace(mediaURL) == "" {
		return nil, "", errors.New("missing media URL")
	}
	if login == nil || login.Bridge == nil || login.Bridge.Bot == nil {
		return nil, "", errors.New("bridge is unavailable")
	}

	var data []byte
	errMediaTooLarge := errors.New("media exceeds max size")
	err := login.Bridge.Bot.DownloadMediaToFile(ctx, id.ContentURIString(mediaURL), encFile, false, func(f *os.File) error {
		var reader io.Reader = f
		if maxBytes > 0 {
			reader = io.LimitReader(f, maxBytes+1)
		}
		var err error
		data, err = io.ReadAll(reader)
		if err != nil {
			return err
		}
		if maxBytes > 0 && int64(len(data)) > maxBytes {
			return errMediaTooLarge
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return data, http.DetectContentType(data), nil
}

// DownloadAndEncodeMedia downloads media from a Matrix content URI, enforces an
// optional size limit, and returns the base64-encoded content.
func DownloadAndEncodeMedia(ctx context.Context, login *bridgev2.UserLogin, mediaURL string, encFile *event.EncryptedFileInfo, maxMB int) (string, string, error) {
	maxBytes := int64(0)
	if maxMB > 0 {
		maxBytes = int64(maxMB) * 1024 * 1024
	}
	data, mimeType, err := DownloadMediaBytes(ctx, login, mediaURL, encFile, maxBytes)
	if err != nil {
		return "", "", err
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return base64.StdEncoding.EncodeToString(data), mimeType, nil
}
