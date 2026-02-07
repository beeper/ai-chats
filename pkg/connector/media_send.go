package connector

import (
	"context"
	"errors"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func (oc *AIClient) sendGeneratedMedia(
	ctx context.Context,
	portal *bridgev2.Portal,
	data []byte,
	mimeType string,
	turnID string,
	msgType event.MessageType,
	fileName string,
	metadataKey string,
	asVoice bool,
) (id.EventID, string, error) {
	intent := oc.getModelIntent(ctx, portal)
	if intent == nil {
		return "", "", errors.New("failed to get model intent")
	}

	uri, file, err := intent.UploadMedia(ctx, portal.MXID, data, fileName, mimeType)
	if err != nil {
		return "", "", fmt.Errorf("upload failed: %w", err)
	}

	info := map[string]any{
		"mimetype": mimeType,
		"size":     len(data),
	}

	rawContent := map[string]any{
		"msgtype": msgType,
		"body":    fileName,
		"info":    info,
	}

	if file != nil {
		rawContent["file"] = file
	} else {
		rawContent["url"] = string(uri)
	}

	if msgType == event.MsgAudio {
		if durationMs, waveform := analyzeAudio(data, mimeType); durationMs > 0 || len(waveform) > 0 {
			if durationMs > 0 {
				info["duration"] = durationMs
			}
			rawContent["org.matrix.msc1767.audio"] = map[string]any{
				"duration": durationMs,
				"waveform": waveform,
			}
		}
		if asVoice {
			rawContent["org.matrix.msc3245.voice"] = map[string]any{}
		}
	}

	if turnID != "" && metadataKey != "" {
		rawContent[metadataKey] = map[string]any{
			"turn_id": turnID,
		}
	}

	eventContent := &event.Content{Raw: rawContent}
	resp, err := intent.SendMessage(ctx, portal.MXID, event.EventMessage, eventContent, nil)
	if err != nil {
		return "", "", fmt.Errorf("send failed: %w", err)
	}
	return resp.EventID, string(uri), nil
}

func extensionForMIME(mimeType, defaultExt string, overrides map[string]string) string {
	if ext, ok := overrides[mimeType]; ok {
		return ext
	}
	return defaultExt
}
