package bridgeadapter

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// DownloadAndEncodeMedia downloads media from a Matrix content URI, enforces an
// optional size limit, and returns the base64-encoded content.
func DownloadAndEncodeMedia(ctx context.Context, login *bridgev2.UserLogin, mediaURL string, encFile *event.EncryptedFileInfo, maxMB int) (string, string, error) {
	if strings.TrimSpace(mediaURL) == "" {
		return "", "", errors.New("missing media URL")
	}
	if login == nil || login.Bridge == nil || login.Bridge.Bot == nil {
		return "", "", errors.New("bridge is unavailable")
	}
	maxBytes := int64(0)
	if maxMB > 0 {
		maxBytes = int64(maxMB) * 1024 * 1024
	}
	var encoded string
	errMediaTooLarge := errors.New("media exceeds max size")
	err := login.Bridge.Bot.DownloadMediaToFile(ctx, id.ContentURIString(mediaURL), encFile, false, func(f *os.File) error {
		var reader io.Reader = f
		if maxBytes > 0 {
			reader = io.LimitReader(f, maxBytes+1)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			return err
		}
		if maxBytes > 0 && int64(len(data)) > maxBytes {
			return errMediaTooLarge
		}
		encoded = base64.StdEncoding.EncodeToString(data)
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return encoded, "application/octet-stream", nil
}
