package ai

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	runtimeparse "github.com/beeper/agentremote/pkg/runtime"
)

func (oc *AIClient) matrixRoomDisplayName(ctx context.Context, portal *bridgev2.Portal) string {
	if portal == nil || oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.Matrix == nil {
		if portal != nil {
			return portal.MXID.String()
		}
		return ""
	}
	if info, err := getMatrixRoomInfo(ctx, &BridgeToolContext{Client: oc, Portal: portal}); err == nil && info != nil {
		if info.Name != "" {
			return info.Name
		}
	}
	name := portalRoomName(portal)
	if name != "" {
		return name
	}
	return portal.MXID.String()
}

func (oc *AIClient) resolveBotMXID(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) id.UserID {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return ""
	}
	if portal != nil && portal.OtherUserID != "" {
		if ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, portal.OtherUserID); err == nil && ghost != nil {
			return ghost.Intent.GetMXID()
		}
	}
	modelID := oc.effectiveModel(meta)
	if modelID != "" {
		if ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, modelUserID(modelID)); err == nil && ghost != nil {
			return ghost.Intent.GetMXID()
		}
	}
	return ""
}

func (oc *AIClient) isCommandAuthorizedSender(sender id.UserID) bool {
	if oc == nil || oc.UserLogin == nil {
		return false
	}
	return oc.UserLogin.UserMXID != "" && sender == oc.UserLogin.UserMXID
}

func (oc *AIClient) buildMatrixInboundBody(
	_ context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	evt *event.Event,
	rawBody string,
	senderName string,
	roomName string,
	isGroup bool,
) string {
	normalized := oc.buildMatrixInboundContext(portal, evt, rawBody, senderName, roomName, isGroup)
	return strings.TrimSpace(normalized.BodyForAgent)
}

func (oc *AIClient) buildMatrixInboundContext(
	portal *bridgev2.Portal,
	evt *event.Event,
	rawBody string,
	senderName string,
	roomName string,
	isGroup bool,
) runtimeparse.InboundContext {
	replyCtx := extractInboundReplyContext(evt)
	messageID := ""
	if evt != nil && evt.ID != "" {
		messageID = evt.ID.String()
	}
	senderID := strings.TrimSpace(senderName)
	if evt != nil && evt.Sender != "" {
		senderID = evt.Sender.String()
	}

	bodyForAgent := rawBody
	if isGroup && senderName != "" && !hasSenderPrefix(strings.TrimSpace(bodyForAgent), senderName) {
		bodyForAgent = senderName + ": " + strings.TrimSpace(bodyForAgent)
	}

	chatID := ""
	if portal != nil && portal.MXID != "" {
		chatID = portal.MXID.String()
	}
	if chatID == "" {
		chatID = strings.TrimSpace(roomName)
	}

	inbound := runtimeparse.InboundContext{
		Provider:          "matrix",
		Surface:           "beeper-matrix",
		ChatType:          chatTypeLabel(isGroup),
		ChatID:            chatID,
		ConversationLabel: strings.TrimSpace(roomName),
		SenderLabel:       strings.TrimSpace(senderName),
		SenderID:          senderID,
		MessageID:         messageID,
		MessageIDFull:     messageID,
		ReplyToID:         replyCtx.ReplyTo.String(),
		ThreadID:          replyCtx.ThreadRoot.String(),
		Body:              rawBody,
		RawBody:           rawBody,
		BodyForAgent:      bodyForAgent,
		BodyForCommands:   rawBody,
	}
	return runtimeparse.FinalizeInboundContext(inbound)
}

func chatTypeLabel(isGroup bool) string {
	if isGroup {
		return "group"
	}
	return "direct"
}
