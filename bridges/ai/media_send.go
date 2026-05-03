package ai

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/sdk"
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
	if portal == nil || portal.MXID == "" {
		return "", "", fmt.Errorf("invalid portal")
	}
	sender := oc.senderForPortal(ctx, portal)
	intent, ok := portal.GetIntentFor(ctx, sender, oc.UserLogin, bridgev2.RemoteEventMessage)
	if !ok || intent == nil {
		return "", "", fmt.Errorf("intent resolution failed")
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

		eventID, _, sendErr := sdk.SendViaPortal(sdk.SendViaPortalParams{
			Login:       oc.UserLogin,
			Portal:      portal,
			Sender:      sender,
			IDPrefix:    oc.ClientBase.MessageIDPrefix,
			LogKey:      oc.ClientBase.MessageLogKey,
			Timestamp:   time.Now(),
			StreamOrder: 0,
			Converted:   converted,
		})
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

	eventID, _, sendErr := sdk.SendViaPortal(sdk.SendViaPortalParams{
		Login:       oc.UserLogin,
		Portal:      portal,
		Sender:      sender,
		IDPrefix:    oc.ClientBase.MessageIDPrefix,
		LogKey:      oc.ClientBase.MessageLogKey,
		Timestamp:   time.Now(),
		StreamOrder: 0,
		Converted:   converted,
	})
	if sendErr != nil {
		return "", "", fmt.Errorf("send failed: %w", sendErr)
	}
	return eventID, string(uri), nil
}

func populateAudioMessageContent(content *event.MessageEventContent, data []byte, mimeType string, asVoice bool, msgType event.MessageType) {
	if msgType != event.MsgAudio {
		return
	}
	if asVoice {
		content.MSC3245Voice = &event.MSC3245Voice{}
	}
}
