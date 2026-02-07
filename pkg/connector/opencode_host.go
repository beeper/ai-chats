package connector

import (
	"context"
	"errors"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/opencodebridge"
)

var _ opencodebridge.Host = (*AIClient)(nil)

func (oc *AIClient) Log() *zerolog.Logger {
	if oc == nil {
		logger := zerolog.Nop()
		return &logger
	}
	return &oc.log
}

func (oc *AIClient) Login() *bridgev2.UserLogin {
	if oc == nil {
		return nil
	}
	return oc.UserLogin
}

func (oc *AIClient) BackgroundContext(ctx context.Context) context.Context {
	if oc == nil {
		return ctx
	}
	return oc.backgroundContext(ctx)
}

func (oc *AIClient) SendSystemNotice(ctx context.Context, portal *bridgev2.Portal, msg string) {
	if oc == nil {
		return
	}
	oc.sendSystemNotice(ctx, portal, msg)
}

func (oc *AIClient) SendPendingStatus(ctx context.Context, portal *bridgev2.Portal, evt *event.Event, msg string) {
	if oc == nil {
		return
	}
	oc.sendPendingStatus(ctx, portal, evt, msg)
}

func (oc *AIClient) SendSuccessStatus(ctx context.Context, portal *bridgev2.Portal, evt *event.Event) {
	if oc == nil {
		return
	}
	oc.sendSuccessStatus(ctx, portal, evt)
}

func (oc *AIClient) EmitOpenCodeStreamEvent(ctx context.Context, portal *bridgev2.Portal, turnID, agentID, targetEventID string, part map[string]any) {
	if oc == nil || portal == nil || portal.MXID == "" || turnID == "" {
		return
	}
	if part == nil {
		return
	}
	intent := oc.getModelIntent(ctx, portal)
	if intent == nil {
		return
	}
	ephemeralSender, ok := intent.(matrixEphemeralSender)
	if !ok {
		partType, _ := part["type"].(string)
		oc.loggerForContext(ctx).Warn().
			Str("part_type", partType).
			Msg("Matrix intent does not support ephemeral events; OpenCode stream updates will be dropped")
		return
	}
	seq := oc.nextOpenCodeStreamSeq(turnID)
	content := map[string]any{
		"turn_id": turnID,
		"seq":     seq,
		"part":    part,
	}
	if targetEventID != "" {
		content["target_event"] = targetEventID
		content["m.relates_to"] = map[string]any{
			"rel_type": RelReference,
			"event_id": targetEventID,
		}
	}
	if agentID != "" {
		content["agent_id"] = agentID
	}
	eventContent := &event.Content{Raw: content}
	txnID := buildStreamEventTxnID(turnID, seq)
	if _, err := ephemeralSender.SendEphemeralEvent(ctx, portal.MXID, StreamEventMessageType, eventContent, txnID); err != nil {
		partType, _ := part["type"].(string)
		oc.loggerForContext(ctx).Warn().Err(err).
			Str("part_type", partType).
			Int("seq", seq).
			Msg("Failed to emit OpenCode stream event")
	}
}

func (oc *AIClient) FinishOpenCodeStream(turnID string) {
	if oc == nil || turnID == "" {
		return
	}
	oc.openCodeStreamMu.Lock()
	defer oc.openCodeStreamMu.Unlock()
	if oc.openCodeStreamSeq != nil {
		delete(oc.openCodeStreamSeq, turnID)
	}
}

func (oc *AIClient) nextOpenCodeStreamSeq(turnID string) int {
	if oc == nil || turnID == "" {
		return 0
	}
	oc.openCodeStreamMu.Lock()
	defer oc.openCodeStreamMu.Unlock()
	if oc.openCodeStreamSeq == nil {
		oc.openCodeStreamSeq = make(map[string]int)
	}
	oc.openCodeStreamSeq[turnID]++
	return oc.openCodeStreamSeq[turnID]
}

func (oc *AIClient) DownloadAndEncodeMedia(ctx context.Context, mediaURL string, file *event.EncryptedFileInfo, maxMB int) (string, string, error) {
	if oc == nil {
		return "", "", errors.New("missing message content")
	}
	return oc.downloadAndEncodeMedia(ctx, mediaURL, file, maxMB)
}

func (oc *AIClient) SetRoomName(ctx context.Context, portal *bridgev2.Portal, name string) error {
	if oc == nil {
		return nil
	}
	return oc.setRoomName(ctx, portal, name)
}

func (oc *AIClient) SenderForOpenCode(instanceID string, fromMe bool) bridgev2.EventSender {
	if oc == nil || oc.UserLogin == nil {
		return bridgev2.EventSender{}
	}
	if fromMe {
		return bridgev2.EventSender{
			Sender:      humanUserID(oc.UserLogin.ID),
			SenderLogin: oc.UserLogin.ID,
			IsFromMe:    true,
		}
	}
	senderID := opencodebridge.OpenCodeUserID(instanceID)
	return bridgev2.EventSender{
		Sender:      senderID,
		SenderLogin: oc.UserLogin.ID,
		IsFromMe:    false,
		ForceDMUser: true,
	}
}

func (oc *AIClient) CleanupPortal(ctx context.Context, portal *bridgev2.Portal, reason string) {
	if oc == nil || portal == nil {
		return
	}
	cleanupPortal(ctx, oc, portal, reason)
}

func (oc *AIClient) PortalMeta(portal *bridgev2.Portal) *opencodebridge.PortalMeta {
	if portal == nil {
		return nil
	}
	meta, _ := portal.Metadata.(*PortalMetadata)
	if meta == nil {
		return &opencodebridge.PortalMeta{}
	}
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

func (oc *AIClient) SetPortalMeta(portal *bridgev2.Portal, meta *opencodebridge.PortalMeta) {
	if portal == nil {
		return
	}
	existing, _ := portal.Metadata.(*PortalMetadata)
	if existing == nil {
		existing = &PortalMetadata{}
	}
	if meta != nil {
		existing.IsOpenCodeRoom = meta.IsOpenCodeRoom
		existing.OpenCodeInstanceID = meta.InstanceID
		existing.OpenCodeSessionID = meta.SessionID
		existing.OpenCodeReadOnly = meta.ReadOnly
		existing.OpenCodeTitlePending = meta.TitlePending
		existing.Title = meta.Title
		existing.TitleGenerated = meta.TitleGenerated
		if meta.AgentID != "" {
			existing.AgentID = meta.AgentID
		}
		if meta.VerboseLevel != "" {
			existing.VerboseLevel = meta.VerboseLevel
		}
	}
	portal.Metadata = existing
}

func (oc *AIClient) SavePortal(ctx context.Context, portal *bridgev2.Portal) error {
	if portal == nil {
		return nil
	}
	return portal.Save(ctx)
}

func (oc *AIClient) DefaultAgentID() string {
	return agents.DefaultAgentID
}

func (oc *AIClient) OpenCodeInstances() map[string]*opencodebridge.OpenCodeInstance {
	if oc == nil || oc.UserLogin == nil {
		return nil
	}
	meta := loginMetadata(oc.UserLogin)
	if meta == nil {
		return nil
	}
	return meta.OpenCodeInstances
}

func (oc *AIClient) SaveOpenCodeInstances(ctx context.Context, instances map[string]*opencodebridge.OpenCodeInstance) error {
	if oc == nil || oc.UserLogin == nil {
		return nil
	}
	meta := loginMetadata(oc.UserLogin)
	if meta == nil {
		return nil
	}
	meta.OpenCodeInstances = instances
	return oc.UserLogin.Save(ctx)
}

func (oc *AIClient) HumanUserID(loginID networkid.UserLoginID) networkid.UserID {
	return humanUserID(loginID)
}

func (oc *AIClient) RoomCapabilitiesEventType() event.Type {
	return RoomCapabilitiesEventType
}

func (oc *AIClient) RoomSettingsEventType() event.Type {
	return RoomSettingsEventType
}

// HandleMatrixDeleteChat deletes the remote OpenCode session when a chat is deleted.
func (oc *AIClient) HandleMatrixDeleteChat(ctx context.Context, msg *bridgev2.MatrixDeleteChat) error {
	if oc == nil || oc.opencodeBridge == nil {
		return nil
	}
	return oc.opencodeBridge.HandleMatrixDeleteChat(ctx, msg)
}
