package opencode

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"

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

func (oc *OpenCodeClient) EmitOpenCodeStreamEvent(ctx context.Context, portal *bridgev2.Portal, turnID, agentID, targetEventID string, part map[string]any) {
	if oc == nil || portal == nil || portal.MXID == "" {
		return
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" || part == nil {
		return
	}
	if oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.Bot == nil {
		return
	}

	oc.streamMu.Lock()
	state := oc.streamStates[turnID]
	if state == nil {
		state = &openCodeStreamState{
			turnID:        turnID,
			agentID:       strings.TrimSpace(agentID),
			targetEventID: strings.TrimSpace(targetEventID),
		}
		oc.streamStates[turnID] = state
	}
	if state.targetEventID == "" && strings.TrimSpace(targetEventID) != "" {
		state.targetEventID = strings.TrimSpace(targetEventID)
	}
	needPlaceholder := state.initialEventID == ""
	partType, _ := part["type"].(string)
	switch strings.TrimSpace(partType) {
	case "text-delta":
		if delta, _ := part["delta"].(string); delta != "" {
			state.visible.WriteString(delta)
			state.accumulated.WriteString(delta)
		}
	case "reasoning-delta":
		if delta, _ := part["delta"].(string); delta != "" {
			state.accumulated.WriteString(delta)
		}
	}
	oc.streamMu.Unlock()

	if needPlaceholder {
		content := &event.Content{Raw: map[string]any{
			"msgtype": event.MsgText,
			"body":    "...",
		}}
		resp, err := oc.UserLogin.Bridge.Bot.SendMessage(ctx, portal.MXID, event.EventMessage, content, nil)
		if err == nil && resp != nil && resp.EventID != "" {
			oc.streamMu.Lock()
			st := oc.streamStates[turnID]
			if st != nil && st.initialEventID == "" {
				st.initialEventID = resp.EventID
				st.targetEventID = resp.EventID.String()
			}
			oc.streamMu.Unlock()
		}
	}

	oc.streamMu.Lock()
	state = oc.streamStates[turnID]
	if state == nil {
		state = &openCodeStreamState{
			turnID:        turnID,
			agentID:       strings.TrimSpace(agentID),
			targetEventID: strings.TrimSpace(targetEventID),
		}
		oc.streamStates[turnID] = state
	}
	session := oc.streamSessions[turnID]
	if session == nil {
		session = streamtransport.NewStreamSession(streamtransport.StreamSessionParams{
			TurnID:  turnID,
			AgentID: state.agentID,
			GetTargetEventID: func() string {
				oc.streamMu.Lock()
				defer oc.streamMu.Unlock()
				st := oc.streamStates[turnID]
				if st == nil {
					return ""
				}
				return st.targetEventID
			},
			GetRoomID: func() id.RoomID {
				return portal.MXID
			},
			GetSuppressSend: func() bool { return false },
			NextSeq: func() int {
				oc.streamMu.Lock()
				defer oc.streamMu.Unlock()
				st := oc.streamStates[turnID]
				if st == nil {
					return 0
				}
				st.sequenceNum++
				return st.sequenceNum
			},
			RuntimeFallbackFlag: &oc.streamFallbackToDebounced,
			GetEphemeralSender: func(callCtx context.Context) (matrixevents.MatrixEphemeralSender, bool) {
				ephemeralSender, ok := any(oc.UserLogin.Bridge.Bot).(matrixevents.MatrixEphemeralSender)
				return ephemeralSender, ok
			},
			SendDebouncedEdit: func(callCtx context.Context, force bool) error {
				oc.streamMu.Lock()
				st := oc.streamStates[turnID]
				var visibleBody, fallbackBody string
				var initialEventID id.EventID
				if st != nil {
					visibleBody = st.visible.String()
					fallbackBody = st.accumulated.String()
					initialEventID = st.initialEventID
				}
				oc.streamMu.Unlock()
				streamtransport.SendDebouncedEdit(callCtx, streamtransport.DebouncedEditParams{
					Portal:         portal,
					Force:          force,
					SuppressSend:   false,
					VisibleBody:    visibleBody,
					FallbackBody:   fallbackBody,
					InitialEventID: initialEventID,
					TurnID:  turnID,
					Intent:  oc.UserLogin.Bridge.Bot,
					Log:            oc.Log(),
				})
				return nil
			},
			Logger: oc.Log(),
		})
		oc.streamSessions[turnID] = session
	}
	oc.streamMu.Unlock()
	session.EmitPart(ctx, part)
}

func (oc *OpenCodeClient) FinishOpenCodeStream(turnID string) {
	if turnID == "" {
		return
	}
	oc.streamMu.Lock()
	session := oc.streamSessions[turnID]
	delete(oc.streamSessions, turnID)
	delete(oc.streamStates, turnID)
	oc.streamMu.Unlock()
	if session != nil {
		session.End(oc.BackgroundContext(context.Background()), streamtransport.EndReasonFinish)
	}
}

func (oc *OpenCodeClient) DownloadAndEncodeMedia(ctx context.Context, mediaURL string, file *event.EncryptedFileInfo, maxMB int) (string, string, error) {
	if strings.TrimSpace(mediaURL) == "" {
		return "", "", errors.New("missing media URL")
	}
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.Bot == nil {
		return "", "", errors.New("bridge is unavailable")
	}
	maxBytes := int64(0)
	if maxMB > 0 {
		maxBytes = int64(maxMB) * 1024 * 1024
	}
	var encoded string
	errMediaTooLarge := errors.New("media exceeds max size")
	err := oc.UserLogin.Bridge.Bot.DownloadMediaToFile(ctx, id.ContentURIString(mediaURL), file, false, func(f *os.File) error {
		var reader io.Reader = f
		if maxBytes > 0 {
			reader = io.LimitReader(f, maxBytes+1)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			return err
		}
		if maxBytes > 0 && int64(len(data)) > maxBytes {
			return errMediaTooLarge
		}
		encoded = base64.StdEncoding.EncodeToString(data)
		return nil
	})
	if errors.Is(err, errMediaTooLarge) {
		return "", "", errMediaTooLarge
	}
	if err != nil {
		return "", "", err
	}
	return encoded, "application/octet-stream", nil
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
