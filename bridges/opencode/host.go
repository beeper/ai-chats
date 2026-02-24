package opencode

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/bridges/opencode/opencodebridge"
	"github.com/beeper/ai-bridge/pkg/matrixevents"
	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
)

var _ opencodebridge.Host = (*OpenCodeClient)(nil)

func (oc *OpenCodeClient) Log() *zerolog.Logger {
	if oc == nil || oc.UserLogin == nil {
		l := zerolog.Nop()
		return &l
	}
	l := oc.UserLogin.Log.With().Str("component", "opencode").Logger()
	return &l
}

func (oc *OpenCodeClient) Login() *bridgev2.UserLogin {
	return oc.UserLogin
}

func (oc *OpenCodeClient) BackgroundContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	if oc != nil && oc.UserLogin != nil && oc.UserLogin.Bridge != nil {
		if bg := oc.UserLogin.Bridge.BackgroundCtx; bg != nil {
			return bg
		}
	}
	return context.Background()
}

func (oc *OpenCodeClient) SendSystemNotice(ctx context.Context, portal *bridgev2.Portal, msg string) {
	if portal == nil || portal.MXID == "" {
		return
	}
	content := &event.MessageEventContent{MsgType: event.MsgNotice, Body: msg, Mentions: &event.Mentions{}}
	_, _ = oc.UserLogin.Bridge.Bot.SendMessage(ctx, portal.MXID, event.EventMessage, &event.Content{Parsed: content}, nil)
}

func (oc *OpenCodeClient) SendPendingStatus(_ context.Context, _ *bridgev2.Portal, _ *event.Event, _ string) {
}

func (oc *OpenCodeClient) SendSuccessStatus(_ context.Context, _ *bridgev2.Portal, _ *event.Event) {
}

func (oc *OpenCodeClient) streamTransportMode() streamtransport.Mode {
	if oc == nil || oc.connector == nil {
		return streamtransport.DefaultMode
	}
	if strings.TrimSpace(oc.connector.Config.Bridge.StreamingTransport) == string(streamtransport.ModeDebouncedEdit) {
		return streamtransport.ModeDebouncedEdit
	}
	return streamtransport.ModeEphemeral
}

func (oc *OpenCodeClient) nextStreamSeq(turnID string) int {
	oc.streamSeqMu.Lock()
	defer oc.streamSeqMu.Unlock()
	oc.streamSeq[turnID]++
	return oc.streamSeq[turnID]
}

func (oc *OpenCodeClient) EmitOpenCodeStreamEvent(ctx context.Context, portal *bridgev2.Portal, turnID, agentID, targetEventID string, part map[string]any) {
	if oc == nil || portal == nil || portal.MXID == "" {
		return
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" || part == nil {
		return
	}
	// OpenCode currently maps turn content via timeline events too; when debounced_edit is selected
	// we disable ephemeral stream transport until replace-target mapping is added here.
	if oc.streamTransportMode() == streamtransport.ModeDebouncedEdit {
		return
	}
	if oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.Bot == nil {
		return
	}
	ephemeralSender, ok := any(oc.UserLogin.Bridge.Bot).(matrixevents.MatrixEphemeralSender)
	if !ok {
		return
	}
	seq := oc.nextStreamSeq(turnID)
	content, err := matrixevents.BuildStreamEventEnvelope(turnID, seq, part, matrixevents.StreamEventOpts{
		TargetEventID: strings.TrimSpace(targetEventID),
		AgentID:       strings.TrimSpace(agentID),
	})
	if err != nil {
		return
	}
	eventContent := &event.Content{Raw: content}
	txnID := matrixevents.BuildStreamEventTxnID(turnID, seq)
	if _, err = ephemeralSender.SendEphemeralEvent(ctx, portal.MXID, matrixevents.StreamEventMessageType, eventContent, txnID); err != nil {
		time.Sleep(100 * time.Millisecond)
		_, _ = ephemeralSender.SendEphemeralEvent(ctx, portal.MXID, matrixevents.StreamEventMessageType, eventContent, txnID)
	}
}

func (oc *OpenCodeClient) FinishOpenCodeStream(turnID string) {
	if turnID == "" {
		return
	}
	oc.streamSeqMu.Lock()
	delete(oc.streamSeq, turnID)
	oc.streamSeqMu.Unlock()
}

func (oc *OpenCodeClient) DownloadAndEncodeMedia(ctx context.Context, mediaURL string, file *event.EncryptedFileInfo, maxMB int) (string, string, error) {
	if strings.TrimSpace(mediaURL) == "" {
		return "", "", errors.New("missing media URL")
	}
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.Bot == nil {
		return "", "", errors.New("bridge is unavailable")
	}
	data, err := oc.UserLogin.Bridge.Bot.DownloadMedia(ctx, id.ContentURIString(mediaURL), file)
	if err != nil {
		return "", "", err
	}
	if maxMB > 0 && len(data) > maxMB*1024*1024 {
		return "", "", errors.New("media exceeds max size")
	}
	return base64.StdEncoding.EncodeToString(data), "application/octet-stream", nil
}

func (oc *OpenCodeClient) SetRoomName(_ context.Context, _ *bridgev2.Portal, _ string) error {
	return nil
}

func (oc *OpenCodeClient) SenderForOpenCode(instanceID string, fromMe bool) bridgev2.EventSender {
	if fromMe {
		return bridgev2.EventSender{Sender: humanUserID(oc.UserLogin.ID), SenderLogin: oc.UserLogin.ID, IsFromMe: true}
	}
	return bridgev2.EventSender{
		Sender:      opencodebridge.OpenCodeUserID(instanceID),
		SenderLogin: oc.UserLogin.ID,
		IsFromMe:    false,
		ForceDMUser: true,
	}
}

func (oc *OpenCodeClient) CleanupPortal(ctx context.Context, portal *bridgev2.Portal, reason string) {
	if portal == nil {
		return
	}
	if portal.MXID != "" {
		if err := portal.Delete(ctx); err != nil {
			oc.UserLogin.Log.Warn().Err(err).Str("portal_id", string(portal.PortalKey.ID)).Str("reason", reason).Msg("Failed to delete portal room")
		}
	}
	if err := oc.UserLogin.Bridge.DB.Portal.Delete(ctx, portal.PortalKey); err != nil {
		oc.UserLogin.Log.Warn().Err(err).Str("portal_id", string(portal.PortalKey.ID)).Str("reason", reason).Msg("Failed to delete portal record")
	}
}

func (oc *OpenCodeClient) PortalMeta(portal *bridgev2.Portal) *opencodebridge.PortalMeta {
	if portal == nil {
		return nil
	}
	meta := portalMeta(portal)
	return &opencodebridge.PortalMeta{
		IsOpenCodeRoom: meta.IsOpenCodeRoom,
		InstanceID:     meta.OpenCodeInstanceID,
		SessionID:      meta.OpenCodeSessionID,
		ReadOnly:       meta.OpenCodeReadOnly,
		TitlePending:   meta.OpenCodeTitlePending,
		Title:          meta.Title,
		TitleGenerated: meta.TitleGenerated,
		AgentID:        meta.AgentID,
		VerboseLevel:   meta.VerboseLevel,
	}
}

func (oc *OpenCodeClient) SetPortalMeta(portal *bridgev2.Portal, meta *opencodebridge.PortalMeta) {
	if portal == nil || meta == nil {
		return
	}
	existing := portalMeta(portal)
	existing.IsOpenCodeRoom = meta.IsOpenCodeRoom
	existing.OpenCodeInstanceID = meta.InstanceID
	existing.OpenCodeSessionID = meta.SessionID
	existing.OpenCodeReadOnly = meta.ReadOnly
	existing.OpenCodeTitlePending = meta.TitlePending
	existing.Title = meta.Title
	existing.TitleGenerated = meta.TitleGenerated
	existing.AgentID = meta.AgentID
	existing.VerboseLevel = meta.VerboseLevel
	portal.Metadata = existing
}

func (oc *OpenCodeClient) SavePortal(ctx context.Context, portal *bridgev2.Portal) error {
	if portal == nil {
		return nil
	}
	return portal.Save(ctx)
}

func (oc *OpenCodeClient) DefaultAgentID() string {
	return "opencode"
}

func (oc *OpenCodeClient) OpenCodeInstances() map[string]*opencodebridge.OpenCodeInstance {
	if oc == nil || oc.UserLogin == nil {
		return nil
	}
	meta := loginMetadata(oc.UserLogin)
	if meta == nil {
		return nil
	}
	return meta.OpenCodeInstances
}

func (oc *OpenCodeClient) SaveOpenCodeInstances(ctx context.Context, instances map[string]*opencodebridge.OpenCodeInstance) error {
	if oc == nil || oc.UserLogin == nil {
		return nil
	}
	meta := loginMetadata(oc.UserLogin)
	if meta == nil {
		return errors.New("missing login metadata")
	}
	meta.OpenCodeInstances = instances
	return oc.UserLogin.Save(ctx)
}

func (oc *OpenCodeClient) HumanUserID(loginID networkid.UserLoginID) networkid.UserID {
	return humanUserID(loginID)
}

func (oc *OpenCodeClient) RoomCapabilitiesEventType() event.Type {
	return matrixevents.RoomCapabilitiesEventType
}

func (oc *OpenCodeClient) RoomSettingsEventType() event.Type {
	return matrixevents.RoomSettingsEventType
}
