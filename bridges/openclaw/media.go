package openclaw

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/shared/jsonutil"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

const openClawMaxMediaMB = 50

type openClawUploadedAttachment struct {
	Content   *event.MessageEventContent
	Metadata  map[string]any
	MatrixURL string
}

func (oc *OpenClawClient) buildOpenClawAttachmentContent(ctx context.Context, portal *bridgev2.Portal, block map[string]any) (*openClawUploadedAttachment, error) {
	if portal == nil || oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.Bot == nil {
		return nil, errors.New("matrix API unavailable")
	}
	source := openClawAttachmentSourceFromBlock(block)
	if source == nil {
		return nil, errors.New("unsupported attachment source")
	}
	data, mimeType, err := downloadOpenClawAttachment(ctx, source, openClawMaxMediaMB)
	if err != nil {
		return nil, err
	}
	filename := openClawAttachmentFilename(source)
	if filename == "" {
		filename = fallbackFilenameForMIME(mimeType)
	}
	uri, file, err := oc.UserLogin.Bridge.Bot.UploadMedia(ctx, portal.MXID, data, filename, mimeType)
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
	matrixURL := string(uri)
	if file != nil {
		content.File = file
		matrixURL = string(file.URL)
	} else {
		content.URL = uri
	}
	return &openClawUploadedAttachment{
		Content:   content,
		Metadata:  openClawMessageExtra(content),
		MatrixURL: matrixURL,
	}, nil
}

type openClawAttachmentSource struct {
	Kind     string
	URL      string
	Data     string
	MimeType string
	FileName string
}

func openClawAttachmentSourceFromBlock(block map[string]any) *openClawAttachmentSource {
	if len(block) == 0 {
		return nil
	}
	for _, candidate := range []any{
		block["source"],
		block["file"],
		block["image_url"],
		block["imageUrl"],
		block["asset"],
		block["blob"],
		block["src"],
	} {
		if source := openClawAttachmentSourceFromValue(candidate, block); source != nil {
			return source
		}
	}
	if data := strings.TrimSpace(stringValue(block["content"])); data != "" {
		return &openClawAttachmentSource{
			Kind:     openClawAttachmentKindFromString(data),
			Data:     data,
			MimeType: openClawBlockMimeType(block),
			FileName: openClawBlockFilename(block),
		}
	}
	if data := strings.TrimSpace(stringValue(block["data"])); data != "" {
		return &openClawAttachmentSource{
			Kind:     openClawAttachmentKindFromString(data),
			Data:     data,
			MimeType: openClawBlockMimeType(block),
			FileName: openClawBlockFilename(block),
		}
	}
	if rawURL := strings.TrimSpace(stringsTrimDefault(stringValue(block["url"]), stringValue(block["href"]))); rawURL != "" {
		return &openClawAttachmentSource{
			Kind:     "url",
			URL:      rawURL,
			MimeType: openClawBlockMimeType(block),
			FileName: openClawBlockFilename(block),
		}
	}
	return nil
}

func openClawAttachmentSourceFromValue(value any, block map[string]any) *openClawAttachmentSource {
	if raw := strings.TrimSpace(stringValue(value)); raw != "" {
		source := &openClawAttachmentSource{
			Kind:     openClawAttachmentKindFromString(raw),
			MimeType: openClawBlockMimeType(block),
			FileName: openClawBlockFilename(block),
		}
		if source.Kind == "url" {
			source.URL = raw
		} else {
			source.Data = raw
		}
		return source
	}

	source := jsonutil.ToMap(value)
	if len(source) == 0 {
		return nil
	}
	for _, nestedKey := range []string{"source", "file", "image_url", "imageUrl", "asset", "blob", "src"} {
		if nested := openClawAttachmentSourceFromValue(source[nestedKey], block); nested != nil {
			return nested
		}
	}
	sourceType := strings.ToLower(strings.TrimSpace(stringValue(source["type"])))
	if sourceType == "" {
		if rawURL := strings.TrimSpace(stringsTrimDefault(stringValue(source["url"]), stringValue(source["href"]))); rawURL != "" {
			sourceType = "url"
		} else if rawData := strings.TrimSpace(stringsTrimDefault(stringValue(source["data"]), stringValue(source["content"]))); rawData != "" {
			sourceType = openClawAttachmentKindFromString(rawData)
		}
	}
	result := &openClawAttachmentSource{
		Kind:     sourceType,
		URL:      strings.TrimSpace(stringsTrimDefault(stringValue(source["url"]), stringValue(source["href"]))),
		Data:     strings.TrimSpace(stringsTrimDefault(stringValue(source["data"]), stringValue(source["content"]))),
		MimeType: openClawSourceMimeType(source, block),
		FileName: stringsTrimDefault(stringsTrimDefault(stringsTrimDefault(stringValue(source["filename"]), stringValue(source["fileName"])), stringsTrimDefault(stringValue(source["name"]), stringValue(source["path"]))), openClawBlockFilename(block)),
	}
	switch result.Kind {
	case "base64", "url":
		return result
	case "":
		return nil
	default:
		if result.URL != "" {
			result.Kind = "url"
			return result
		}
		if result.Data != "" {
			result.Kind = openClawAttachmentKindFromString(result.Data)
			return result
		}
		return nil
	}
}

func openClawAttachmentKindFromString(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "file://") || strings.HasPrefix(raw, "/") {
		return "url"
	}
	if strings.HasPrefix(raw, "data:") {
		return "base64"
	}
	return "base64"
}

func openClawBlockFilename(block map[string]any) string {
	for _, key := range []string{"fileName", "filename", "name", "title", "path"} {
		if value := strings.TrimSpace(stringValue(block[key])); value != "" {
			return value
		}
	}
	return ""
}

func openClawBlockMimeType(block map[string]any) string {
	return stringutil.NormalizeMimeType(
		stringsTrimDefault(
			stringsTrimDefault(
				stringsTrimDefault(stringValue(block["contentType"]), stringValue(block["mimeType"])),
				stringValue(block["mime_type"]),
			),
			stringsTrimDefault(stringValue(block["mediaType"]), stringValue(block["media_type"])),
		),
	)
}

func openClawSourceMimeType(source, block map[string]any) string {
	return stringutil.NormalizeMimeType(
		stringsTrimDefault(
			stringsTrimDefault(
				stringsTrimDefault(stringValue(source["contentType"]), stringValue(source["mimeType"])),
				stringValue(source["mime_type"]),
			),
			stringsTrimDefault(
				stringsTrimDefault(stringValue(source["mediaType"]), stringValue(source["media_type"])),
				openClawBlockMimeType(block),
			),
		),
	)
}

func openClawAttachmentFilename(source *openClawAttachmentSource) string {
	if source == nil {
		return ""
	}
	if source.FileName != "" {
		return source.FileName
	}
	if source.URL == "" {
		return ""
	}
	if strings.HasPrefix(source.URL, "file://") {
		pathValue := strings.TrimPrefix(source.URL, "file://")
		if unescaped, err := url.PathUnescape(pathValue); err == nil {
			pathValue = unescaped
		}
		return filepath.Base(pathValue)
	}
	if strings.HasPrefix(source.URL, "/") {
		return filepath.Base(source.URL)
	}
	parsed, err := url.Parse(source.URL)
	if err != nil {
		return ""
	}
	base := path.Base(parsed.Path)
	if base == "." || base == "/" {
		return ""
	}
	return base
}

func downloadOpenClawAttachment(ctx context.Context, source *openClawAttachmentSource, maxSizeMB int) ([]byte, string, error) {
	if source == nil {
		return nil, "", errors.New("missing attachment source")
	}
	maxBytes := int64(maxSizeMB * 1024 * 1024)
	switch source.Kind {
	case "base64":
		data, mimeType, err := decodeOpenClawDataOrBase64(source.Data, source.MimeType)
		if err != nil {
			return nil, "", err
		}
		if maxBytes > 0 && int64(len(data)) > maxBytes {
			return nil, "", fmt.Errorf("file too large: %d bytes (max %d MB)", len(data), maxSizeMB)
		}
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		return data, mimeType, nil
	case "url":
		return downloadOpenClawAttachmentURL(ctx, source.URL, source.MimeType, maxBytes, maxSizeMB)
	default:
		return nil, "", fmt.Errorf("unsupported attachment source kind %q", source.Kind)
	}
}

func decodeOpenClawDataOrBase64(raw, fallbackMime string) ([]byte, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, "", errors.New("missing attachment data")
	}
	if strings.HasPrefix(raw, "data:") {
		data, mimeType, err := media.DecodeDataURI(raw)
		if err != nil {
			return nil, "", err
		}
		return data, stringutil.NormalizeMimeType(mimeType), nil
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, "", err
	}
	mimeType := stringutil.NormalizeMimeType(fallbackMime)
	if mimeType == "" {
		mimeType = http.DetectContentType(decoded)
	}
	return decoded, mimeType, nil
}

func downloadOpenClawAttachmentURL(ctx context.Context, rawURL, fallbackMime string, maxBytes int64, maxSizeMB int) ([]byte, string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, "", errors.New("missing attachment URL")
	}
	if strings.HasPrefix(rawURL, "file://") || strings.HasPrefix(rawURL, "/") {
		return nil, "", errors.New("local file access is not permitted")
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
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("file download failed with status %d", resp.StatusCode)
	}
	if maxBytes > 0 && resp.ContentLength > 0 && resp.ContentLength > maxBytes {
		return nil, "", fmt.Errorf("file too large: %d bytes (max %d MB)", resp.ContentLength, maxSizeMB)
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
		return nil, "", fmt.Errorf("file too large: %d bytes (max %d MB)", len(data), maxSizeMB)
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

func messageTypeForMIME(mimeType string) event.MessageType {
	mimeType = stringutil.NormalizeMimeType(mimeType)
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return event.MsgImage
	case strings.HasPrefix(mimeType, "audio/"):
		return event.MsgAudio
	case strings.HasPrefix(mimeType, "video/"):
		return event.MsgVideo
	default:
		return event.MsgFile
	}
}

func fallbackFilenameForMIME(mimeType string) string {
	mimeType = stringutil.NormalizeMimeType(mimeType)
	exts, _ := mime.ExtensionsByType(mimeType)
	if len(exts) > 0 {
		return "file" + exts[0]
	}
	return "file"
}

func openClawMessageExtra(content *event.MessageEventContent) map[string]any {
	extra := map[string]any{
		"msgtype":    content.MsgType,
		"body":       content.Body,
		"m.mentions": map[string]any{},
	}
	if content.FileName != "" {
		extra["filename"] = content.FileName
	}
	if content.Info != nil {
		info := map[string]any{}
		if content.Info.MimeType != "" {
			info["mimetype"] = content.Info.MimeType
		}
		if content.Info.Size > 0 {
			info["size"] = content.Info.Size
		}
		if len(info) > 0 {
			extra["info"] = info
		}
	}
	if content.File != nil {
		extra["file"] = content.File
	} else if content.URL != id.ContentURIString("") {
		extra["url"] = string(content.URL)
	}
	return extra
}
