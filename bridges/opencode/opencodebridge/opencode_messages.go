package opencodebridge

import (
	"context"
	"fmt"
	"mime"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/bridges/opencode/opencode"
	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

const openCodeMaxMediaMB = 50

func (b *Bridge) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage, portal *bridgev2.Portal, meta *PortalMeta) (*bridgev2.MatrixMessageResponse, error) {
	if msg.Content == nil || msg.Event == nil {
		return nil, errMissingMessageContent
	}
	if msg.Content.RelatesTo != nil && msg.Content.RelatesTo.GetReplaceID() != "" {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	if b == nil || b.manager == nil {
		b.host.SendSystemNotice(ctx, portal, "OpenCode integration is not available.")
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	if meta != nil && meta.AwaitingPath {
		return b.handleAwaitingPath(ctx, msg, portal, meta)
	}
	if meta == nil || meta.InstanceID == "" || meta.SessionID == "" {
		b.host.SendSystemNotice(ctx, portal, "OpenCode session metadata is missing for this room.")
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	if meta.ReadOnly || !b.manager.IsConnected(meta.InstanceID) {
		b.host.SendSystemNotice(ctx, portal, "OpenCode is disconnected for this room. Messages are read-only until it reconnects.")
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	msgType := msg.Content.MsgType
	if msg.Event.Type == event.EventSticker {
		msgType = event.MsgImage
	}

	parts, titleCandidate, err := b.buildInboundParts(ctx, msg, msgType)
	if err != nil {
		return nil, err
	}

	runCtx := b.host.BackgroundContext(ctx)
	go func() {
		if err := b.manager.SendMessage(runCtx, meta.InstanceID, meta.SessionID, parts, msg.Event.ID); err != nil {
			b.host.SendSystemNotice(runCtx, portal, "OpenCode send failed: "+err.Error())
			return
		}
		b.maybeFinalizeOpenCodeTitle(runCtx, portal, meta, titleCandidate)
	}()

	return &bridgev2.MatrixMessageResponse{Pending: true}, nil
}

func (b *Bridge) handleAwaitingPath(ctx context.Context, msg *bridgev2.MatrixMessage, portal *bridgev2.Portal, meta *PortalMeta) (*bridgev2.MatrixMessageResponse, error) {
	path := strings.TrimSpace(msg.Content.Body)
	session, err := b.manager.CreateSession(ctx, meta.InstanceID, "", path)
	if err != nil {
		b.host.SendSystemNotice(ctx, portal, "Failed to create session: "+err.Error())
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	meta.SessionID = session.ID
	meta.AwaitingPath = false
	b.host.SetPortalMeta(portal, meta)
	_ = b.host.SavePortal(ctx, portal)
	b.host.SendSystemNotice(ctx, portal, fmt.Sprintf("Working directory set to %s", path))
	return &bridgev2.MatrixMessageResponse{Pending: false}, nil
}

func (b *Bridge) buildInboundParts(ctx context.Context, msg *bridgev2.MatrixMessage, msgType event.MessageType) ([]opencode.PartInput, string, error) {
	switch msgType {
	case event.MsgText, event.MsgNotice, event.MsgEmote:
		body := strings.TrimSpace(msg.Content.Body)
		if body == "" {
			return nil, "", errEmptyMessage
		}
		return []opencode.PartInput{{Type: "text", Text: body}}, body, nil

	case event.MsgImage, event.MsgVideo, event.MsgAudio, event.MsgFile:
		return b.buildMediaParts(ctx, msg)

	default:
		return nil, "", errUnsupportedMessageType
	}
}

func (b *Bridge) buildMediaParts(ctx context.Context, msg *bridgev2.MatrixMessage) ([]opencode.PartInput, string, error) {
	mediaURL := string(msg.Content.URL)
	if mediaURL == "" && msg.Content.File != nil {
		mediaURL = string(msg.Content.File.URL)
	}
	if mediaURL == "" {
		return nil, "", errUnsupportedMessageType
	}
	b64Data, mimeType, err := b.host.DownloadAndEncodeMedia(ctx, mediaURL, msg.Content.File, openCodeMaxMediaMB)
	if err != nil {
		return nil, "", err
	}
	if mimeType == "" && msg.Content.Info != nil {
		mimeType = stringutil.NormalizeMimeType(msg.Content.Info.MimeType)
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	filename := strings.TrimSpace(msg.Content.FileName)
	caption := strings.TrimSpace(msg.Content.Body)
	if filename == "" {
		filename = caption
		caption = ""
	} else if caption == filename {
		caption = ""
	}
	if filename == "" {
		filename = fallbackFilenameForMIME(mimeType)
	}

	dataURL := fmt.Sprintf("data:%s;base64,%s", mimeType, b64Data)
	parts := []opencode.PartInput{{
		Type:     "file",
		Mime:     mimeType,
		Filename: filename,
		URL:      dataURL,
	}}
	if caption != "" {
		parts = append(parts, opencode.PartInput{Type: "text", Text: caption})
	}
	titleCandidate := caption
	if titleCandidate == "" {
		titleCandidate = filename
	}
	return parts, titleCandidate, nil
}

func fallbackFilenameForMIME(mimeType string) string {
	extensions, _ := mime.ExtensionsByType(mimeType)
	if len(extensions) > 0 {
		return "file" + extensions[0]
	}
	return "file"
}

func (b *Bridge) maybeFinalizeOpenCodeTitle(ctx context.Context, portal *bridgev2.Portal, meta *PortalMeta, title string) {
	if b == nil || portal == nil || meta == nil {
		return
	}
	if !meta.TitlePending || meta.InstanceID == "" || meta.SessionID == "" {
		return
	}
	normalized := sanitizeOpenCodeTitle(title)
	if normalized == "" || b.manager == nil {
		return
	}
	if _, err := b.manager.UpdateSessionTitle(ctx, meta.InstanceID, meta.SessionID, normalized); err != nil {
		b.host.Log().Warn().Err(err).Msg("Failed to update OpenCode session title")
		return
	}
	meta.Title = normalized
	meta.TitleGenerated = false
	meta.TitlePending = false
	portal.Name = normalized
	portal.NameSet = true
	b.host.SetPortalMeta(portal, meta)
	if err := b.host.SavePortal(ctx, portal); err != nil {
		b.host.Log().Warn().Err(err).Msg("Failed to save OpenCode portal title")
	}
	if portal.MXID != "" {
		_ = b.host.SetRoomName(ctx, portal, normalized)
	}
}

func sanitizeOpenCodeTitle(title string) string {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.Join(strings.Fields(trimmed), " ")
	if len(trimmed) > 80 {
		trimmed = trimmed[:80] + "..."
	}
	return trimmed
}

func (b *Bridge) emitOpenCodePartRemove(ctx context.Context, portal *bridgev2.Portal, instanceID, partID, partType string, fromMe bool) {
	if portal == nil || partID == "" {
		return
	}
	sender := b.opencodeSender(instanceID, fromMe)
	if partType == "tool" {
		b.emitOpenCodeMessageRemoveWithSender(ctx, portal, opencodeToolCallMessageID(partID), sender)
		b.emitOpenCodeMessageRemoveWithSender(ctx, portal, opencodeToolResultMessageID(partID), sender)
		return
	}
	b.emitOpenCodeMessageRemoveWithSender(ctx, portal, opencodePartMessageID(partID), sender)
}

func (b *Bridge) emitOpenCodeMessageRemove(ctx context.Context, portal *bridgev2.Portal, instanceID, messageID string, fromMe bool) {
	if portal == nil || messageID == "" {
		return
	}
	sender := b.opencodeSender(instanceID, fromMe)
	b.emitOpenCodeMessageRemoveWithSender(ctx, portal, networkid.MessageID("opencode:"+messageID), sender)
}

func (b *Bridge) emitOpenCodeMessageRemoveWithSender(_ context.Context, portal *bridgev2.Portal, messageID networkid.MessageID, sender bridgev2.EventSender) {
	if portal == nil || messageID == "" || b == nil || b.host == nil {
		return
	}
	login := b.host.Login()
	if login == nil {
		return
	}
	login.QueueRemoteEvent(&simplevent.MessageRemove{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventMessageRemove,
			PortalKey: portal.PortalKey,
			Sender:    sender,
			Timestamp: time.Now(),
		},
		TargetMessage: messageID,
	})
}

func opencodePartMessageID(partID string) networkid.MessageID {
	return networkid.MessageID("opencode:part:" + partID)
}

func opencodeToolCallMessageID(partID string) networkid.MessageID {
	return networkid.MessageID("opencode:toolcall:" + partID)
}

func opencodeToolResultMessageID(partID string) networkid.MessageID {
	return networkid.MessageID("opencode:toolresult:" + partID)
}

func (b *Bridge) opencodeSender(instanceID string, fromMe bool) bridgev2.EventSender {
	if b == nil || b.host == nil {
		return bridgev2.EventSender{}
	}
	return b.host.SenderForOpenCode(instanceID, fromMe)
}

var (
	errMissingMessageContent  = bridgeError("missing message content")
	errUnsupportedMessageType = bridgeError("unsupported message type")
	errEmptyMessage           = bridgeError("empty message body")
)
