package ai

import (
	"context"
	"fmt"
	"time"

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

	info := &event.FileInfo{
		MimeType: mimeType,
		Size:     len(data),
	}

	body := caption
	content := &event.MessageEventContent{
		MsgType:  msgType,
		Body:     body,
		FileName: fileName,
		Info:     info,
		Mentions: &event.Mentions{},
	}
	if file != nil {
		content.File = file
	} else {
		content.URL = uri
	}

	if msgType == event.MsgImage {
		if w, h := analyzeImage(data); w > 0 && h > 0 {
			info.Width = w
			info.Height = h
		}
	}

	if msgType == event.MsgVideo {
		if w, h, dur := analyzeVideo(ctx, data); w > 0 && h > 0 {
			info.Width = w
			info.Height = h
			if dur > 0 {
				info.Duration = dur
			}
		}
	}

	populateAudioMessageContent(content, data, mimeType, asVoice, msgType)

	if turnID != "" && metadataKey != "" {
		rawMetadata := map[string]any{
			"turn_id": turnID,
		}
		converted := &bridgev2.ConvertedMessage{
			Parts: []*bridgev2.ConvertedMessagePart{{
				ID:         networkid.PartID("0"),
				Type:       event.EventMessage,
				Content:    content,
				Extra:      map[string]any{metadataKey: rawMetadata},
				DBMetadata: nil,
			}},
		}

		eventID, _, sendErr := oc.sendViaPortalWithTiming(ctx, portal, converted, "", time.Now(), 0)
		if sendErr != nil {
			return "", "", fmt.Errorf("send failed: %w", sendErr)
		}
		return eventID, string(uri), nil
	}

	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:      networkid.PartID("0"),
			Type:    event.EventMessage,
			Content: content,
		}},
	}

	eventID, _, sendErr := oc.sendViaPortalWithTiming(ctx, portal, converted, "", time.Now(), 0)
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

func populateAudioMessageContent(content *event.MessageEventContent, data []byte, mimeType string, asVoice bool, msgType event.MessageType) {
	if msgType != event.MsgAudio {
		return
	}
	if durationMs, waveform := analyzeAudio(data, mimeType); durationMs > 0 || len(waveform) > 0 {
		content.MSC1767Audio = &event.MSC1767Audio{
			Duration: durationMs,
			Waveform: waveform,
		}
	}
	if asVoice {
		content.MSC3245Voice = &event.MSC3245Voice{}
	}
}
