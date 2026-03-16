package ai

import (
	"context"
	"errors"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
)

func sendFormattedMessage(ctx context.Context, btc *BridgeToolContext, message string, relatesTo map[string]any, errorPrefix string) (id.EventID, error) {
	if btc.Portal == nil || btc.Portal.MXID == "" {
		return "", errors.New("invalid portal")
	}

	rendered := format.RenderMarkdown(message, true, true)
	raw := map[string]any{
		"msgtype":        event.MsgText,
		"body":           rendered.Body,
		"format":         rendered.Format,
		"formatted_body": rendered.FormattedBody,
		"m.mentions":     map[string]any{},
	}
	if relatesTo != nil {
		raw["m.relates_to"] = relatesTo
	}

	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:      networkid.PartID("0"),
			Type:    event.EventMessage,
			Content: &event.MessageEventContent{MsgType: event.MsgText, Body: rendered.Body},
			Extra:   raw,
		}},
	}

	eventID, _, err := btc.Client.sendViaPortal(ctx, btc.Portal, converted, "")
	if err != nil {
		if errorPrefix == "" {
			errorPrefix = "failed to send message"
		}
		return "", fmt.Errorf("%s: %w", errorPrefix, err)
	}
	return eventID, nil
}
