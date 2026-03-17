package opencode

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

var _ Host = (*OpenCodeClient)(nil)

func (oc *OpenCodeClient) Log() *zerolog.Logger {
	if oc == nil || oc.UserLogin == nil {
		l := zerolog.Nop()
		return &l
	}
	l := oc.UserLogin.Log.With().Str("component", "opencode").Logger()
	return &l
}

func (oc *OpenCodeClient) SendSystemNotice(ctx context.Context, portal *bridgev2.Portal, msg string) {
	if portal == nil || portal.MXID == "" {
		return
	}
	oc.sendSystemNoticeViaPortal(ctx, portal, msg)
}

func (oc *OpenCodeClient) EmitOpenCodeStreamEvent(ctx context.Context, portal *bridgev2.Portal, turnID, agentID string, part map[string]any) {
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
	if oc.IsStreamShuttingDown() {
		return
	}

	agentID = strings.TrimSpace(agentID)
	ctx = oc.BackgroundContext(ctx)

	state, turn := oc.ensureStreamTurn(ctx, portal, turnID, agentID)
	if state == nil || turn == nil {
		return
	}
	oc.streamHost.Lock()
	if metadata, _ := part["messageMetadata"].(map[string]any); len(metadata) > 0 {
		oc.applyStreamMessageMetadata(state, metadata)
	}
	state.stream.ApplyPart(part, time.Time{})
	oc.streamHost.Unlock()

	if oc.IsStreamShuttingDown() || turn == nil {
		return
	}
	bridgesdk.ApplyStreamPart(turn, part, bridgesdk.PartApplyOptions{
		ResetMetadataOnStartMarkers:     true,
		ResetMetadataOnEmptyMessageMeta: true,
		ResetMetadataOnEmptyTextDelta:   true,
		ResetMetadataOnAbort:            true,
		ResetMetadataOnDataParts:        true,
		HandleTerminalEvents:            true,
		DefaultFinishReason:             "stop",
	})
}

func (oc *OpenCodeClient) ensureStreamTurn(ctx context.Context, portal *bridgev2.Portal, turnID, agentID string) (*openCodeStreamState, *bridgesdk.Turn) {
	if oc == nil || portal == nil || portal.MXID == "" {
		return nil, nil
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" || oc.IsStreamShuttingDown() {
		return nil, nil
	}
	ctx = oc.BackgroundContext(ctx)
	agentID = strings.TrimSpace(agentID)

	oc.streamHost.Lock()
	defer oc.streamHost.Unlock()

	state := oc.streamHost.GetLocked(turnID)
	if state == nil {
		state = &openCodeStreamState{
			portal:  portal,
			turnID:  turnID,
			agentID: agentID,
		}
		state.ui.TurnID = turnID
		oc.streamHost.SetLocked(turnID, state)
	}
	if state.portal == nil {
		state.portal = portal
	}
	if state.agentID == "" {
		state.agentID = agentID
	}
	if state.turn == nil {
		state.turn = oc.newSDKStreamTurn(ctx, portal, state)
	}
	return state, state.turn
}

func (oc *OpenCodeClient) ensureStreamWriter(ctx context.Context, portal *bridgev2.Portal, turnID, agentID string) (*openCodeStreamState, *bridgesdk.Writer) {
	state, turn := oc.ensureStreamTurn(ctx, portal, turnID, agentID)
	if state == nil || turn == nil {
		return state, nil
	}
	return state, turn.Writer()
}

func (oc *OpenCodeClient) FinishOpenCodeStream(turnID string) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return
	}
	oc.streamHost.Lock()
	oc.streamHost.DeleteLocked(turnID)
	oc.streamHost.Unlock()
}

func (oc *OpenCodeClient) newSDKStreamTurn(ctx context.Context, portal *bridgev2.Portal, state *openCodeStreamState) *bridgesdk.Turn {
	if oc == nil || portal == nil || state == nil || oc.connector == nil || oc.connector.sdkConfig == nil {
		return nil
	}
	pmeta := oc.PortalMeta(portal)
	var instanceID string
	if pmeta != nil {
		instanceID = pmeta.InstanceID
	}
	agent := openCodeSDKAgent(instanceID, oc.instanceDisplayName(instanceID))
	if state.agentID != "" {
		agent.ID = state.agentID
	}
	sender := oc.SenderForOpenCode(instanceID, false)
	conv := bridgesdk.NewConversation(ctx, oc.UserLogin, portal, sender, oc.connector.sdkConfig, oc)
	_ = conv.EnsureRoomAgent(ctx, agent)
	turn := conv.StartTurn(ctx, agent, nil)
	turn.SetID(state.turnID)
	turn.SetSender(sender)
	turn.SetFinalMetadataProvider(bridgesdk.FinalMetadataProviderFunc(func(_ *bridgesdk.Turn, finishReason string) any {
		return oc.buildSDKFinalMetadata(state, finishReason)
	}))
	return turn
}

func (oc *OpenCodeClient) DownloadAndEncodeMedia(ctx context.Context, mediaURL string, file *event.EncryptedFileInfo, maxMB int) (string, string, error) {
	return agentremote.DownloadAndEncodeMedia(ctx, oc.UserLogin, mediaURL, file, maxMB)
}

func (oc *OpenCodeClient) SetRoomName(_ context.Context, _ *bridgev2.Portal, _ string) error {
	return nil
}

func (oc *OpenCodeClient) SenderForOpenCode(instanceID string, fromMe bool) bridgev2.EventSender {
	if fromMe {
		return bridgev2.EventSender{Sender: humanUserID(oc.UserLogin.ID), SenderLogin: oc.UserLogin.ID, IsFromMe: true}
	}
	return bridgev2.EventSender{
		Sender:      OpenCodeUserID(instanceID),
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

func (oc *OpenCodeClient) PortalMeta(portal *bridgev2.Portal) *PortalMeta {
	if portal == nil {
		return nil
	}
	meta := portalMeta(portal)
	return &PortalMeta{
		IsOpenCodeRoom: meta.IsOpenCodeRoom,
		InstanceID:     meta.OpenCodeInstanceID,
		SessionID:      meta.OpenCodeSessionID,
		ReadOnly:       meta.OpenCodeReadOnly,
		TitlePending:   meta.OpenCodeTitlePending,
		Title:          meta.Title,
		TitleGenerated: meta.TitleGenerated,
		AgentID:        meta.AgentID,
		VerboseLevel:   meta.VerboseLevel,
		AwaitingPath:   meta.OpenCodeAwaitingPath,
	}
}

func (oc *OpenCodeClient) SetPortalMeta(portal *bridgev2.Portal, meta *PortalMeta) {
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
	existing.OpenCodeAwaitingPath = meta.AwaitingPath
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

func (oc *OpenCodeClient) OpenCodeInstances() map[string]*OpenCodeInstance {
	if oc == nil || oc.UserLogin == nil {
		return nil
	}
	meta := loginMetadata(oc.UserLogin)
	if meta == nil {
		return nil
	}
	return meta.OpenCodeInstances
}

func (oc *OpenCodeClient) SaveOpenCodeInstances(ctx context.Context, instances map[string]*OpenCodeInstance) error {
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
