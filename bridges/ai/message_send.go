package ai

import (
	"context"
	"errors"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
)

func buildMessageRelatesTo(replyToEventID, threadRootEventID id.EventID) *event.RelatesTo {
	if threadRootEventID != "" {
		return (&event.RelatesTo{}).SetThread(threadRootEventID, replyToEventID)
	}
	if replyToEventID != "" {
		return (&event.RelatesTo{}).SetReplyTo(replyToEventID)
	}
	return nil
}

func sendFormattedMessage(ctx context.Context, btc *BridgeToolContext, message string, relatesTo *event.RelatesTo, errorPrefix string) (id.EventID, error) {
	if btc.Portal == nil || btc.Portal.MXID == "" {
		return "", errors.New("invalid portal")
	}

	rendered := format.RenderMarkdown(message, true, true)
	content := &event.MessageEventContent{
		MsgType:       event.MsgText,
		Body:          rendered.Body,
		Format:        rendered.Format,
		FormattedBody: rendered.FormattedBody,
		Mentions:      &event.Mentions{},
		RelatesTo:     relatesTo,
	}
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:      networkid.PartID("0"),
			Type:    event.EventMessage,
			Content: content,
		}},
	}

	eventID, _, err := btc.Client.sendViaPortalWithTiming(ctx, btc.Portal, converted, "", time.Now(), 0)
	if err != nil {
		if errorPrefix == "" {
			errorPrefix = "failed to send message"
		}
		return "", fmt.Errorf("%s: %w", errorPrefix, err)
	}
	return eventID, nil
}
