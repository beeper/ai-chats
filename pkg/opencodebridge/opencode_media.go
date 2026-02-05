package opencodebridge

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/opencode"
)

func (b *Bridge) buildOpenCodeFileContent(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, part opencode.Part) (*event.MessageEventContent, error) {
	if portal == nil || intent == nil {
		return nil, fmt.Errorf("matrix API unavailable")
	}
	fileURL := strings.TrimSpace(part.URL)
	if fileURL == "" {
		return nil, fmt.Errorf("missing file URL")
	}
	data, mimeType, err := downloadOpenCodeFile(ctx, fileURL, part.Mime, openCodeMaxMediaMB)
	if err != nil {
		return nil, err
	}
	if part.Mime != "" {
		mimeType = normalizeMimeType(part.Mime)
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
		return nil, "", fmt.Errorf("missing file URL")
	}
	if strings.HasPrefix(fileURL, "data:") {
		data, mimeType, err := decodeOpenCodeDataURL(fileURL)
		if err != nil {
			return nil, "", err
		}
		if maxSizeMB > 0 {
			maxBytes := int64(maxSizeMB * 1024 * 1024)
			if int64(len(data)) > maxBytes {
				return nil, "", fmt.Errorf("file too large: %d bytes (max %d MB)", len(data), maxSizeMB)
			}
		}
		if mimeType == "" {
			mimeType = normalizeMimeType(fallbackMime)
		}
		return data, mimeType, nil
	}

	if strings.HasPrefix(fileURL, "file://") || strings.HasPrefix(fileURL, "/") {
		pathValue := fileURL
		if strings.HasPrefix(pathValue, "file://") {
			pathValue = strings.TrimPrefix(pathValue, "file://")
			if unescaped, err := url.PathUnescape(pathValue); err == nil {
				pathValue = unescaped
			}
		}
		info, err := os.Stat(pathValue)
		if err != nil {
			return nil, "", fmt.Errorf("failed to stat file: %w", err)
		}
		if maxSizeMB > 0 {
			maxBytes := int64(maxSizeMB * 1024 * 1024)
			if info.Size() > maxBytes {
				return nil, "", fmt.Errorf("file too large: %d bytes (max %d MB)", info.Size(), maxSizeMB)
			}
		}
		data, err := os.ReadFile(pathValue)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read file: %w", err)
		}
		mimeType := normalizeMimeType(mime.TypeByExtension(filepath.Ext(pathValue)))
		if mimeType == "" {
			mimeType = http.DetectContentType(data)
		}
		if mimeType == "" {
			mimeType = normalizeMimeType(fallbackMime)
		}
		return data, mimeType, nil
	}

	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("file download failed with status %d", resp.StatusCode)
	}
	if maxSizeMB > 0 && resp.ContentLength > 0 {
		maxBytes := int64(maxSizeMB * 1024 * 1024)
		if resp.ContentLength > maxBytes {
			return nil, "", fmt.Errorf("file too large: %d bytes (max %d MB)", resp.ContentLength, maxSizeMB)
		}
	}
	var reader io.Reader = resp.Body
	if maxSizeMB > 0 {
		maxBytes := int64(maxSizeMB * 1024 * 1024)
		reader = io.LimitReader(resp.Body, maxBytes+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", err
	}
	if maxSizeMB > 0 {
		maxBytes := int64(maxSizeMB * 1024 * 1024)
		if int64(len(data)) > maxBytes {
			return nil, "", fmt.Errorf("file too large: %d bytes (max %d MB)", len(data), maxSizeMB)
		}
	}
	mimeType := normalizeMimeType(resp.Header.Get("Content-Type"))
	if mimeType == "" {
		mimeType = normalizeMimeType(fallbackMime)
	}
	return data, mimeType, nil
}

func decodeOpenCodeDataURL(raw string) ([]byte, string, error) {
	if !strings.HasPrefix(raw, "data:") {
		return nil, "", fmt.Errorf("not a data URL")
	}
	comma := strings.IndexByte(raw, ',')
	if comma < 0 {
		return nil, "", fmt.Errorf("invalid data URL")
	}
	meta := raw[len("data:"):comma]
	payload := raw[comma+1:]
	isBase64 := strings.Contains(meta, ";base64")
	mimeType := ""
	if meta != "" {
		mimeType = strings.TrimSpace(strings.Split(meta, ";")[0])
	}
	if isBase64 {
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil, "", err
		}
		return decoded, normalizeMimeType(mimeType), nil
	}
	decoded, err := url.PathUnescape(payload)
	if err != nil {
		return nil, "", err
	}
	return []byte(decoded), normalizeMimeType(mimeType), nil
}

func filenameFromOpenCodeURL(raw string) string {
	if strings.HasPrefix(raw, "file://") {
		pathValue := strings.TrimPrefix(raw, "file://")
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
