package connector

import (
	"context"
	"errors"
	"fmt"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
)

func sendFormattedMessage(ctx context.Context, btc *BridgeToolContext, message string, relatesTo map[string]any, errorPrefix string) (id.EventID, error) {
	intent := btc.Client.getModelIntent(ctx, btc.Portal)
	if intent == nil {
		return "", errors.New("failed to get model intent")
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

	eventContent := &event.Content{Raw: raw}
	resp, err := intent.SendMessage(ctx, btc.Portal.MXID, event.EventMessage, eventContent, nil)
	if err != nil {
		if errorPrefix == "" {
			errorPrefix = "failed to send message"
		}
		return "", fmt.Errorf("%s: %w", errorPrefix, err)
	}
	return resp.EventID, nil
}
