package opencode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/bridges/opencode/api"
	"github.com/beeper/agentremote/pkg/shared/media"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/sdk"
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
	if meta != nil && meta.roomPhase().AwaitingPath() {
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
	cfg := b.InstanceConfig(meta.InstanceID)
	if cfg == nil || cfg.Mode != OpenCodeModeManagedLauncher {
		b.host.SendSystemNotice(ctx, portal, "This room is no longer waiting for a managed OpenCode path.")
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	path, err := resolveManagedWorkingDirectory(msg.Content.Body, cfg.DefaultDirectory)
	if err != nil {
		b.host.SendSystemNotice(ctx, portal, err.Error())
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		b.host.SendSystemNotice(ctx, portal, fmt.Sprintf("That path doesn't exist or isn't a directory: %s", path))
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	inst, err := b.manager.EnsureManagedInstance(ctx, meta.InstanceID, path)
	if err != nil {
		b.host.SendSystemNotice(ctx, portal, "Failed to start managed OpenCode: "+err.Error())
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	session, err := b.manager.CreateSession(ctx, inst.cfg.ID, "", "")
	if err != nil {
		b.host.SendSystemNotice(ctx, portal, "Failed to create session: "+err.Error())
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	if !openCodeSessionUsesDirectory(path, session) {
		if deleteErr := b.manager.DeleteSession(ctx, inst.cfg.ID, session.ID); deleteErr != nil {
			b.host.Log().Warn().Err(deleteErr).Str("session_id", session.ID).Msg("Failed to delete managed OpenCode session created with unexpected directory")
		}
		actualDir := strings.TrimSpace(session.Directory)
		if actualDir == "" {
			b.host.SendSystemNotice(ctx, portal, fmt.Sprintf("Managed OpenCode created the session without reporting a working directory. Requested %s.", path))
		} else {
			b.host.SendSystemNotice(ctx, portal, fmt.Sprintf("Managed OpenCode created the session in %s instead of %s.", actualDir, path))
		}
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	portal, err = b.ReIDPortalToSession(ctx, portal, inst.cfg.ID, session.ID)
	if err != nil {
		b.host.SendSystemNotice(ctx, portal, "Failed to attach the room to the managed OpenCode session: "+err.Error())
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	meta = b.applyOpenCodePortalMeta(meta, openCodePortalMetaUpdate{
		setSessionID:  true,
		sessionID:     session.ID,
		setInstanceID: true,
		instanceID:    inst.cfg.ID,
		setPhase:      true,
		phase:         meta.roomPhase().AfterSessionAttach(),
		setReadOnly:   true,
		readOnly:      false,
		ensureAgent:   true,
	})
	portal, _, _, err = b.bootstrapOpenCodePortal(ctx, nil, portal, strings.TrimSpace(meta.Title), meta, false)
	if err != nil {
		b.host.SendSystemNotice(ctx, portal, "Failed to save the managed OpenCode session room: "+err.Error())
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	b.host.SendSystemNotice(ctx, portal, fmt.Sprintf("Managed OpenCode started in %s", session.Directory))
	return &bridgev2.MatrixMessageResponse{Pending: false}, nil
}

func resolveManagedWorkingDirectory(raw, defaultDir string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		path = strings.TrimSpace(defaultDir)
	}
	if path == "" {
		return "", errors.New("send an absolute path or `~/...`, or configure a default path in the managed OpenCode login")
	}
	path, err := sdk.NormalizeAbsolutePath(path)
	if err != nil {
		return "", errors.New("send an absolute path or `~/...` for managed OpenCode")
	}
	return path, nil
}

func openCodeSessionUsesDirectory(requested string, session *api.Session) bool {
	if session == nil {
		return false
	}
	requested = strings.TrimSpace(requested)
	actual := strings.TrimSpace(session.Directory)
	if requested == "" || actual == "" {
		return false
	}
	return filepath.Clean(actual) == filepath.Clean(requested)
}

func (b *Bridge) buildInboundParts(ctx context.Context, msg *bridgev2.MatrixMessage, msgType event.MessageType) ([]api.PartInput, string, error) {
	switch msgType {
	case event.MsgText, event.MsgNotice, event.MsgEmote:
		body := strings.TrimSpace(msg.Content.Body)
		if body == "" {
			return nil, "", errEmptyMessage
		}
		return []api.PartInput{{Type: "text", Text: body}}, body, nil

	case event.MsgImage, event.MsgVideo, event.MsgAudio, event.MsgFile:
		return b.buildMediaParts(ctx, msg)

	default:
		return nil, "", errUnsupportedMessageType
	}
}

func (b *Bridge) buildMediaParts(ctx context.Context, msg *bridgev2.MatrixMessage) ([]api.PartInput, string, error) {
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
		filename = media.FallbackFilenameForMIME(mimeType)
	}

	dataURL := fmt.Sprintf("data:%s;base64,%s", mimeType, b64Data)
	parts := []api.PartInput{{
		Type:     "file",
		Mime:     mimeType,
		Filename: filename,
		URL:      dataURL,
	}}
	if caption != "" {
		parts = append(parts, api.PartInput{Type: "text", Text: caption})
	}
	titleCandidate := caption
	if titleCandidate == "" {
		titleCandidate = filename
	}
	return parts, titleCandidate, nil
}

func (b *Bridge) maybeFinalizeOpenCodeTitle(ctx context.Context, portal *bridgev2.Portal, meta *PortalMeta, title string) {
	if b == nil || portal == nil || meta == nil {
		return
	}
	if !meta.roomPhase().TitlePending() || meta.InstanceID == "" || meta.SessionID == "" {
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
	meta = b.applyOpenCodePortalMeta(meta, openCodePortalMetaUpdate{
		setTitle: true,
		title:    normalized,
		setPhase: true,
		phase:    openCodePortalPhaseReady,
	})
	portal.Name = normalized
	portal.NameSet = true
	b.host.SetPortalMeta(portal, meta)
	if err := b.host.SavePortal(ctx, portal); err != nil {
		b.host.Log().Warn().Err(err).Msg("Failed to save OpenCode portal title")
	}
}

func sanitizeOpenCodeTitle(title string) string {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return ""
	}
	return stringutil.Truncate(strings.Join(strings.Fields(trimmed), " "), 80)
}

func (b *Bridge) emitOpenCodePartRemove(ctx context.Context, portal *bridgev2.Portal, instanceID, partID, partType string, fromMe bool) {
	if portal == nil || partID == "" {
		return
	}
	if partType == "tool" {
		return
	}
	sender := b.opencodeSender(instanceID, fromMe)
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
	login := b.host.GetUserLogin()
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

func (b *Bridge) opencodeSender(instanceID string, fromMe bool) bridgev2.EventSender {
	if b == nil || b.host == nil {
		return bridgev2.EventSender{}
	}
	return b.host.SenderForOpenCode(instanceID, fromMe)
}

// HandleMatrixDeleteChat deletes the remote OpenCode session when a chat is deleted.
func (b *Bridge) HandleMatrixDeleteChat(ctx context.Context, msg *bridgev2.MatrixDeleteChat) error {
	if b == nil || msg == nil || msg.Portal == nil {
		return nil
	}
	meta := b.portalMeta(msg.Portal)
	if meta == nil || !meta.IsOpenCodeRoom {
		return nil
	}
	sessionID := strings.TrimSpace(meta.SessionID)
	if !meta.roomPhase().CanDeleteRemoteSession(sessionID) {
		return nil
	}
	if b.manager == nil {
		return nil
	}
	return b.manager.DeleteSession(ctx, meta.InstanceID, sessionID)
}

var (
	errMissingMessageContent  = bridgeError("missing message content")
	errUnsupportedMessageType = bridgeError("unsupported message type")
	errEmptyMessage           = bridgeError("empty message body")
)
