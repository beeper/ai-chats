package connector

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
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
	caption string,
) (id.EventID, string, error) {
	// Get intent for upload (standard pattern — 7 reference bridges use intent.UploadMedia)
	intent, err := oc.getIntentForPortal(ctx, portal, bridgev2.RemoteEventMessage)
	if err != nil {
		return "", "", fmt.Errorf("intent resolution failed: %w", err)
	}

	uri, file, err := intent.UploadMedia(ctx, portal.MXID, data, fileName, mimeType)
	if err != nil {
		return "", "", fmt.Errorf("upload failed: %w", err)
	}

	info := map[string]any{
		"mimetype": mimeType,
		"size":     len(data),
	}

	body := caption

	rawContent := map[string]any{
		"msgtype":    msgType,
		"body":       body,
		"filename":   fileName,
		"info":       info,
		"m.mentions": map[string]any{},
	}

	if file != nil {
		rawContent["file"] = file
	} else {
		rawContent["url"] = string(uri)
	}

	if msgType == event.MsgImage {
		if w, h := analyzeImage(data); w > 0 && h > 0 {
			info["w"] = w
			info["h"] = h
		}
	}

	if msgType == event.MsgVideo {
		if w, h, dur := analyzeVideo(ctx, data); w > 0 && h > 0 {
			info["w"] = w
			info["h"] = h
			if dur > 0 {
				info["duration"] = dur
			}
		}
	}

	populateAudioMessageContent(rawContent, info, data, mimeType, asVoice, msgType)

	if turnID != "" && metadataKey != "" {
		rawContent[metadataKey] = map[string]any{
			"turn_id": turnID,
		}
	}

	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:      networkid.PartID("0"),
			Type:    event.EventMessage,
			Content: &event.MessageEventContent{MsgType: msgType, Body: body},
			Extra:   rawContent,
		}},
	}

	eventID, _, sendErr := oc.sendViaPortal(ctx, portal, converted, "")
	if sendErr != nil {
		return "", "", fmt.Errorf("send failed: %w", sendErr)
	}
	return eventID, string(uri), nil
}

func extensionForMIME(mimeType, defaultExt string, overrides map[string]string) string {
	if ext, ok := overrides[mimeType]; ok {
		return ext
	}
	return defaultExt
}

func populateAudioMessageContent(rawContent map[string]any, info map[string]any, data []byte, mimeType string, asVoice bool, msgType event.MessageType) {
	if msgType != event.MsgAudio {
		return
	}
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
