package connector

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
)

type inboundContextKey struct{}

func withInboundContext(ctx context.Context, inbound airuntime.InboundContext) context.Context {
	return context.WithValue(ctx, inboundContextKey{}, airuntime.FinalizeInboundContext(inbound))
}

func inboundContextFromContext(ctx context.Context) (airuntime.InboundContext, bool) {
	if ctx == nil {
		return airuntime.InboundContext{}, false
	}
	raw := ctx.Value(inboundContextKey{})
	if raw == nil {
		return airuntime.InboundContext{}, false
	}
	inbound, ok := raw.(airuntime.InboundContext)
	if !ok {
		return airuntime.InboundContext{}, false
	}
	return airuntime.FinalizeInboundContext(inbound), true
}

func (oc *AIClient) resolvePromptInboundContext(
	ctx context.Context,
	portal *bridgev2.Portal,
	latest string,
	eventID id.EventID,
) airuntime.InboundContext {
	if inbound, ok := inboundContextFromContext(ctx); ok {
		inbound.BodyForAgent = airuntime.SanitizeChatMessageForDisplay(inbound.BodyForAgent, true)
		inbound.BodyForCommands = airuntime.SanitizeChatMessageForDisplay(inbound.BodyForCommands, true)
		if strings.TrimSpace(inbound.BodyForAgent) == "" {
			inbound.BodyForAgent = strings.TrimSpace(latest)
		}
		if inbound.MessageID == "" && eventID != "" {
			inbound.MessageID = eventID.String()
			inbound.MessageIDFull = eventID.String()
		}
		return airuntime.FinalizeInboundContext(inbound)
	}

	chatType := "direct"
	chatID := ""
	if portal != nil {
		chatID = portal.MXID.String()
		if portal.Portal != nil && portal.Portal.RoomType == database.RoomTypeGroupDM {
			chatType = "group"
		}
	}
	normalized := airuntime.FinalizeInboundContext(airuntime.InboundContext{
		Provider:      "matrix",
		Surface:       "beeper-matrix",
		ChatType:      chatType,
		ChatID:        chatID,
		MessageID:     eventID.String(),
		MessageIDFull: eventID.String(),
		Body:          latest,
		RawBody:       latest,
		BodyForAgent:  airuntime.SanitizeChatMessageForDisplay(latest, true),
	})
	if strings.TrimSpace(normalized.BodyForCommands) == "" {
		normalized.BodyForCommands = normalized.BodyForAgent
	}
	return normalized
}
