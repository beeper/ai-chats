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

	"github.com/beeper/agentremote/sdk"
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

	sender := btc.Client.senderForPortal(ctx, btc.Portal)
	eventID, _, err := sdk.SendViaPortal(sdk.SendViaPortalParams{
		Login:       btc.Client.UserLogin,
		Portal:      btc.Portal,
		Sender:      sender,
		IDPrefix:    btc.Client.ClientBase.MessageIDPrefix,
		LogKey:      btc.Client.ClientBase.MessageLogKey,
		Timestamp:   time.Now(),
		StreamOrder: 0,
		Converted:   converted,
	})
	if err != nil {
		if errorPrefix == "" {
			errorPrefix = "failed to send message"
		}
		return "", fmt.Errorf("%s: %w", errorPrefix, err)
	}
	return eventID, nil
}
