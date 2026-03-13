package opencode

import (
	"context"
	"errors"
	"strings"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/pkg/matrixevents"
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

	oc.StreamMu.Lock()
	state := oc.streamStates[turnID]
	if state == nil {
		state = &openCodeStreamState{
			portal:  portal,
			turnID:  turnID,
			agentID: strings.TrimSpace(agentID),
		}
		state.ui.TurnID = turnID
		oc.streamStates[turnID] = state
	}
	if state.portal == nil {
		state.portal = portal
	}
	if state.agentID == "" {
		state.agentID = agentID
	}
	if metadata, _ := part["messageMetadata"].(map[string]any); len(metadata) > 0 {
		oc.applyStreamMessageMetadata(state, metadata)
	}
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
	case "error":
		if errText, _ := part["errorText"].(string); strings.TrimSpace(errText) != "" {
			state.errorText = strings.TrimSpace(errText)
		}
	case "finish":
		if finishReason, _ := part["finishReason"].(string); strings.TrimSpace(finishReason) != "" {
			state.finishReason = strings.TrimSpace(finishReason)
		}
	case "abort":
		state.finishReason = "abort"
	}
	turn := state.turn
	if turn == nil {
		turn = oc.newSDKStreamTurn(ctx, portal, state)
		state.turn = turn
	}
	oc.StreamMu.Unlock()

	if oc.IsStreamShuttingDown() || turn == nil {
		return
	}
	switch strings.TrimSpace(partType) {
	case "start", "message-metadata":
		if metadata, _ := part["messageMetadata"].(map[string]any); len(metadata) > 0 {
			turn.SetMetadata(metadata)
		} else {
			turn.SetMetadata(nil)
		}
	case "start-step":
		turn.StepStart()
	case "finish-step":
		turn.StepFinish()
	case "text-start", "reasoning-start":
		turn.SetMetadata(nil)
	case "text-delta":
		if delta, _ := part["delta"].(string); delta != "" {
			turn.WriteText(delta)
		} else {
			turn.SetMetadata(nil)
		}
	case "text-end":
		turn.FinishText()
	case "reasoning-delta":
		if delta, _ := part["delta"].(string); delta != "" {
			turn.WriteReasoning(delta)
		} else {
			turn.SetMetadata(nil)
		}
	case "reasoning-end":
		turn.FinishReasoning()
	case "tool-input-start":
		toolName, _ := part["toolName"].(string)
		toolCallID, _ := part["toolCallId"].(string)
		providerExecuted, _ := part["providerExecuted"].(bool)
		turn.ToolStart(toolName, toolCallID, providerExecuted)
	case "tool-input-delta":
		toolCallID, _ := part["toolCallId"].(string)
		inputTextDelta, _ := part["inputTextDelta"].(string)
		turn.ToolInputDelta(toolCallID, inputTextDelta)
	case "tool-input-available":
		toolCallID, _ := part["toolCallId"].(string)
		turn.ToolInput(toolCallID, part["input"])
	case "tool-output-available":
		toolCallID, _ := part["toolCallId"].(string)
		turn.ToolOutput(toolCallID, part["output"])
	case "tool-output-error":
		toolCallID, _ := part["toolCallId"].(string)
		errorText, _ := part["errorText"].(string)
		turn.ToolOutputError(toolCallID, errorText)
	case "tool-output-denied":
		toolCallID, _ := part["toolCallId"].(string)
		turn.ToolDenied(toolCallID)
	case "tool-approval-request":
		turn.SetMetadata(nil)
		approvalID, _ := part["approvalId"].(string)
		toolCallID, _ := part["toolCallId"].(string)
		turn.Emitter().EmitUIToolApprovalRequest(turn.Context(), portal, approvalID, toolCallID)
	case "tool-approval-response":
		turn.SetMetadata(nil)
		approvalID, _ := part["approvalId"].(string)
		toolCallID, _ := part["toolCallId"].(string)
		approved, _ := part["approved"].(bool)
		reason, _ := part["reason"].(string)
		turn.Emitter().EmitUIToolApprovalResponse(turn.Context(), portal, approvalID, toolCallID, approved, reason)
	case "file":
		url, _ := part["url"].(string)
		mediaType, _ := part["mediaType"].(string)
		turn.AddFile(url, mediaType)
	case "source-document":
		sourceID, _ := part["sourceId"].(string)
		title, _ := part["title"].(string)
		mediaType, _ := part["mediaType"].(string)
		filename, _ := part["filename"].(string)
		turn.AddSourceDocument(sourceID, title, mediaType, filename)
	case "source-url":
		url, _ := part["url"].(string)
		title, _ := part["title"].(string)
		turn.AddSourceURL(url, title)
	case "error":
		errText, _ := part["errorText"].(string)
		turn.SetMetadata(nil)
		turn.Emitter().EmitUIError(turn.Context(), portal, errText)
	case "finish":
		turn.SetMetadata(nil)
		finishReason, _ := part["finishReason"].(string)
		if strings.TrimSpace(finishReason) == "" {
			finishReason = "stop"
		}
		turn.End(finishReason)
	case "abort":
		reason, _ := part["reason"].(string)
		turn.SetMetadata(nil)
		turn.Abort(reason)
	default:
		if strings.HasPrefix(strings.TrimSpace(partType), "data-") {
			turn.SetMetadata(nil)
			turn.Emitter().Emit(turn.Context(), portal, part)
		}
	}
}

func (oc *OpenCodeClient) FinishOpenCodeStream(turnID string) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return
	}
	oc.StreamMu.Lock()
	state := oc.streamStates[turnID]
	delete(oc.streamStates, turnID)
	oc.StreamMu.Unlock()
	if state != nil && state.turn != nil {
		finishReason := strings.TrimSpace(state.finishReason)
		if finishReason == "" {
			finishReason = "stop"
		}
		state.turn.End(finishReason)
	}
}

func (oc *OpenCodeClient) newSDKStreamTurn(ctx context.Context, portal *bridgev2.Portal, state *openCodeStreamState) *bridgesdk.Turn {
	if oc == nil || portal == nil || state == nil || oc.connector == nil || oc.connector.sdkConfig == nil {
		return nil
	}
	pmeta := oc.PortalMeta(portal)
	instanceID := ""
	if pmeta != nil {
		instanceID = pmeta.InstanceID
	}
	agent := openCodeSDKAgent(instanceID, oc.instanceDisplayName(instanceID))
	if strings.TrimSpace(state.agentID) != "" {
		agent.ID = strings.TrimSpace(state.agentID)
	}
	sender := oc.SenderForOpenCode(instanceID, false)
	conv := bridgesdk.NewConversation(ctx, oc.UserLogin, portal, sender, oc.connector.sdkConfig, oc)
	_ = conv.EnsureRoomAgent(ctx, agent)
	turn := conv.StartTurn(ctx, agent, nil)
	turn.SetID(state.turnID)
	turn.SetSender(sender)
	turn.SetFinalMetadataBuilder(func(_ *bridgesdk.Turn, finishReason string) any {
		return oc.buildSDKFinalMetadata(state, finishReason)
	})
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

func (oc *OpenCodeClient) RoomCapabilitiesEventType() event.Type {
	return matrixevents.RoomCapabilitiesEventType
}

func (oc *OpenCodeClient) RoomSettingsEventType() event.Type {
	return matrixevents.RoomSettingsEventType
}
