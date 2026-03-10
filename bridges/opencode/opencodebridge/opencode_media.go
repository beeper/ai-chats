package opencodebridge

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/bridges/opencode/opencode"
	"github.com/beeper/agentremote/pkg/shared/media"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

func (b *Bridge) buildOpenCodeFileContent(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, part opencode.Part) (*event.MessageEventContent, error) {
	if portal == nil || intent == nil {
		return nil, errors.New("matrix API unavailable")
	}
	fileURL := strings.TrimSpace(part.URL)
	if fileURL == "" {
		return nil, errors.New("missing file URL")
	}
	data, mimeType, err := downloadOpenCodeFile(ctx, fileURL, part.Mime, openCodeMaxMediaMB)
	if err != nil {
		return nil, err
	}
	if part.Mime != "" {
		mimeType = stringutil.NormalizeMimeType(part.Mime)
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	filename := strings.TrimSpace(part.Filename)
	if filename == "" {
		filename = filenameFromOpenCodeURL(fileURL)
	}
	if filename == "" {
		filename = fallbackFilenameForMIME(mimeType)
	}

	uri, file, err := intent.UploadMedia(ctx, portal.MXID, data, filename, mimeType)
	if err != nil {
		return nil, err
	}

	content := &event.MessageEventContent{
		MsgType:  messageTypeForMIME(mimeType),
		Body:     filename,
		FileName: filename,
		Info: &event.FileInfo{
			MimeType: mimeType,
			Size:     len(data),
		},
	}
	if file != nil {
		content.File = file
	} else {
		content.URL = uri
	}
	return content, nil
}

func downloadOpenCodeFile(ctx context.Context, fileURL, fallbackMime string, maxSizeMB int) ([]byte, string, error) {
	fileURL = strings.TrimSpace(fileURL)
	if fileURL == "" {
		return nil, "", errors.New("missing file URL")
	}
	var maxBytes int64
	if maxSizeMB > 0 {
		maxBytes = int64(maxSizeMB * 1024 * 1024)
	}
	if strings.HasPrefix(fileURL, "data:") {
		data, mimeType, err := media.DecodeDataURI(fileURL)
		if err != nil {
			return nil, "", err
		}
		if maxBytes > 0 && int64(len(data)) > maxBytes {
			return nil, "", fmt.Errorf("file too large: %d bytes (max %d MB)", len(data), maxSizeMB)
		}
		mimeType = stringutil.NormalizeMimeType(mimeType)
		if mimeType == "" {
			mimeType = stringutil.NormalizeMimeType(fallbackMime)
		}
		return data, mimeType, nil
	}

	if strings.HasPrefix(fileURL, "file://") || strings.HasPrefix(fileURL, "/") {
		pathValue := fileURL
		if p, ok := strings.CutPrefix(pathValue, "file://"); ok {
			pathValue = p
			if unescaped, err := url.PathUnescape(pathValue); err == nil {
				pathValue = unescaped
			}
		}
		info, err := os.Stat(pathValue)
		if err != nil {
			return nil, "", fmt.Errorf("failed to stat file: %w", err)
		}
		if maxBytes > 0 && info.Size() > maxBytes {
			return nil, "", fmt.Errorf("file too large: %d bytes (max %d MB)", info.Size(), maxSizeMB)
		}
		data, err := os.ReadFile(pathValue)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read file: %w", err)
		}
		mimeType := stringutil.NormalizeMimeType(mime.TypeByExtension(filepath.Ext(pathValue)))
		if mimeType == "" {
			mimeType = http.DetectContentType(data)
		}
		if mimeType == "" {
			mimeType = stringutil.NormalizeMimeType(fallbackMime)
		}
		return data, mimeType, nil
	}

	return media.DownloadURL(ctx, fileURL, fallbackMime, maxBytes)
}

func filenameFromOpenCodeURL(raw string) string {
	if pathValue, ok := strings.CutPrefix(raw, "file://"); ok {
		if unescaped, err := url.PathUnescape(pathValue); err == nil {
			pathValue = unescaped
		}
		return filepath.Base(pathValue)
	}
	if strings.HasPrefix(raw, "/") {
		return filepath.Base(raw)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	base := path.Base(parsed.Path)
	if base == "." || base == "/" {
		return ""
	}
	return base
}
