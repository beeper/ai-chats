package codex

import (
	"context"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
	"github.com/beeper/ai-bridge/pkg/connector/msgconv"
	"github.com/beeper/ai-bridge/pkg/matrixevents"
	"github.com/beeper/ai-bridge/pkg/shared/citations"
	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
	"github.com/beeper/ai-bridge/pkg/shared/streamui"
)

// --- System / utility messages ---

func (cc *CodexClient) sendSystemNotice(ctx context.Context, portal *bridgev2.Portal, message string) {
	if portal == nil || portal.MXID == "" || cc.UserLogin == nil || cc.UserLogin.Bridge == nil {
		return
	}
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:   networkid.PartID("0"),
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				MsgType:  event.MsgNotice,
				Body:     strings.TrimSpace(message),
				Mentions: &event.Mentions{},
			},
		}},
	}
	sendCtx, cancel := context.WithTimeout(cc.backgroundContext(ctx), 10*time.Second)
	defer cancel()
	cc.sendViaPortal(sendCtx, portal, converted, "")
}

func (cc *CodexClient) sendSystemNoticeOnce(ctx context.Context, portal *bridgev2.Portal, state *streamingState, key string, message string) {
	key = strings.TrimSpace(key)
	if key == "" || state == nil {
		cc.sendSystemNotice(ctx, portal, message)
		return
	}
	if state.codexTimelineNotices == nil {
		state.codexTimelineNotices = make(map[string]bool)
	}
	if state.codexTimelineNotices[key] {
		return
	}
	state.codexTimelineNotices[key] = true
	cc.sendSystemNotice(ctx, portal, message)
}

// --- Message status ---

func (cc *CodexClient) sendPendingStatus(ctx context.Context, portal *bridgev2.Portal, evt *event.Event, message string) {
	if portal == nil || portal.Bridge == nil || evt == nil {
		return
	}
	st := bridgev2.MessageStatus{
		Status:    event.MessageStatusPending,
		Message:   message,
		IsCertain: true,
	}
	portal.Bridge.Matrix.SendMessageStatus(ctx, &st, bridgev2.StatusEventInfoFromEvent(evt))
}

func (cc *CodexClient) markMessageSendSuccess(ctx context.Context, portal *bridgev2.Portal, evt *event.Event, state *streamingState) {
	if portal == nil || portal.Bridge == nil || evt == nil || state == nil {
		return
	}
	st := bridgev2.MessageStatus{Status: event.MessageStatusSuccess, IsCertain: true}
	portal.Bridge.Matrix.SendMessageStatus(ctx, &st, bridgev2.StatusEventInfoFromEvent(evt))
}

// --- Room concurrency ---

func (cc *CodexClient) acquireRoom(roomID id.RoomID) bool {
	cc.roomMu.Lock()
	defer cc.roomMu.Unlock()
	if cc.activeRooms[roomID] {
		return false
	}
	cc.activeRooms[roomID] = true
	return true
}

func (cc *CodexClient) releaseRoom(roomID id.RoomID) {
	cc.roomMu.Lock()
	defer cc.roomMu.Unlock()
	delete(cc.activeRooms, roomID)
}

func (cc *CodexClient) queuePendingCodex(roomID id.RoomID, pm *codexPendingMessage) {
	cc.roomMu.Lock()
	defer cc.roomMu.Unlock()
	cc.pendingMessages[roomID] = pm
}

func (cc *CodexClient) popPendingCodex(roomID id.RoomID) *codexPendingMessage {
	cc.roomMu.Lock()
	defer cc.roomMu.Unlock()
	pm := cc.pendingMessages[roomID]
	delete(cc.pendingMessages, roomID)
	return pm
}

func (cc *CodexClient) processPendingCodex(roomID id.RoomID) {
	cc.roomMu.Lock()
	pm := cc.pendingMessages[roomID]
	cc.roomMu.Unlock()
	if pm == nil {
		return
	}
	ctx := cc.backgroundContext(context.Background())
	if err := cc.ensureRPC(ctx); err != nil {
		cc.log.Warn().Err(err).Stringer("room", roomID).Msg("Pending codex message: RPC unavailable")
		return
	}
	meta := portalMeta(pm.portal)
	if meta == nil {
		cc.popPendingCodex(roomID)
		return
	}
	if err := cc.ensureCodexThreadLoaded(ctx, pm.portal, meta); err != nil {
		cc.log.Warn().Err(err).Stringer("room", roomID).Msg("Pending codex message: thread load failed")
		return
	}
	if !cc.acquireRoom(roomID) {
		return
	}
	cc.popPendingCodex(roomID)
	go func() {
		func() {
			defer cc.releaseRoom(roomID)
			cc.runTurn(ctx, pm.portal, meta, pm.event, pm.body)
		}()
		cc.processPendingCodex(roomID)
	}()
}

// --- Streaming initial message ---

func (cc *CodexClient) sendInitialStreamMessage(ctx context.Context, portal *bridgev2.Portal, state *streamingState, content string, turnID string) id.EventID {
	uiMessage := map[string]any{
		"id":   turnID,
		"role": "assistant",
		"metadata": map[string]any{
			"turn_id": turnID,
		},
		"parts": []any{},
	}
	eventRaw := map[string]any{
		"msgtype":                event.MsgText,
		"body":                   content,
		matrixevents.BeeperAIKey: uiMessage,
		"m.mentions":             map[string]any{},
	}
	msgID := newMessageID()
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:         networkid.PartID("0"),
			Type:       event.EventMessage,
			Content:    &event.MessageEventContent{MsgType: event.MsgText, Body: content},
			Extra:      eventRaw,
			DBMetadata: &MessageMetadata{Role: "assistant", TurnID: turnID},
		}},
	}
	eventID, _, err := cc.sendViaPortal(ctx, portal, converted, msgID)
	if err != nil {
		cc.loggerForContext(ctx).Error().Err(err).Msg("Failed to send initial streaming message")
		return ""
	}
	if state != nil {
		state.networkMessageID = msgID
	}
	cc.loggerForContext(ctx).Info().Stringer("event_id", eventID).Str("turn_id", turnID).Msg("Initial streaming message sent")
	return eventID
}

// --- UI metadata and emission ---

func (cc *CodexClient) buildUIMessageMetadata(state *streamingState, model string, includeUsage bool, finishReason string) map[string]any {
	return msgconv.BuildUIMessageMetadata(msgconv.UIMessageMetadataParams{
		TurnID:           state.turnID,
		AgentID:          state.agentID,
		Model:            strings.TrimSpace(model),
		FinishReason:     finishReason,
		PromptTokens:     state.promptTokens,
		CompletionTokens: state.completionTokens,
		ReasoningTokens:  state.reasoningTokens,
		TotalTokens:      state.totalTokens,
		StartedAtMs:      state.startedAtMs,
		FirstTokenAtMs:   state.firstTokenAtMs,
		CompletedAtMs:    state.completedAtMs,
		IncludeUsage:     includeUsage,
	})
}

func (cc *CodexClient) emitUIStart(ctx context.Context, portal *bridgev2.Portal, state *streamingState, model string) {
	cc.uiEmitter(state).EmitUIStart(ctx, portal, cc.buildUIMessageMetadata(state, model, false, ""))
}

func (cc *CodexClient) ensureUIToolInputStart(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID, toolName string, providerExecuted bool, input any) {
	if toolCallID == "" {
		return
	}
	ui := cc.uiEmitter(state)
	ui.EnsureUIToolInputStart(ctx, portal, toolCallID, toolName, providerExecuted, false, streamui.ToolDisplayTitle(toolName), nil)
	ui.EmitUIToolInputAvailable(ctx, portal, toolCallID, toolName, input, providerExecuted)
}

func (cc *CodexClient) emitUIToolApprovalRequest(
	ctx context.Context, portal *bridgev2.Portal, state *streamingState,
	approvalID, toolCallID, toolName string, ttlSeconds int,
) {
	cc.uiEmitter(state).EmitUIToolApprovalRequest(ctx, portal, approvalID, toolCallID, toolName, ttlSeconds)
}

func (cc *CodexClient) emitUIFinish(ctx context.Context, portal *bridgev2.Portal, state *streamingState, model string, finishReason string) {
	cc.uiEmitter(state).EmitUIFinish(ctx, portal, finishReason, cc.buildUIMessageMetadata(state, model, true, finishReason))
	if state != nil && state.session != nil {
		state.session.End(ctx, streamtransport.EndReason(finishReason))
		state.session = nil
	}
}

// --- Final turn message ---

func (cc *CodexClient) buildCanonicalUIMessage(state *streamingState, model string, finishReason string) map[string]any {
	parts := msgconv.ContentParts(
		strings.TrimSpace(state.accumulated.String()),
		strings.TrimSpace(state.reasoning.String()),
	)
	if toolParts := msgconv.ToolCallParts(state.toolCalls, string(matrixevents.ToolTypeProvider), string(matrixevents.ResultStatusSuccess), string(matrixevents.ResultStatusDenied)); len(toolParts) > 0 {
		parts = append(parts, toolParts...)
	}
	return msgconv.BuildUIMessage(msgconv.UIMessageParams{
		TurnID:     state.turnID,
		Role:       "assistant",
		Metadata:   cc.buildUIMessageMetadata(state, model, true, finishReason),
		Parts:      parts,
		SourceURLs: citations.BuildSourceParts(state.sourceCitations, state.sourceDocuments),
		FileParts:  citations.GeneratedFilesToParts(state.generatedFiles),
	})
}

func (cc *CodexClient) sendFinalAssistantTurn(ctx context.Context, portal *bridgev2.Portal, state *streamingState, model string, finishReason string) {
	if portal == nil || portal.MXID == "" || state == nil || !state.hasInitialMessageTarget() {
		return
	}
	if state.suppressSend {
		return
	}
	rendered := format.RenderMarkdown(state.accumulated.String(), true, true)

	// Safety-split oversized responses into multiple Matrix events.
	var continuationBody string
	if len(rendered.Body) > streamtransport.MaxMatrixEventBodyBytes {
		firstBody, rest := streamtransport.SplitAtMarkdownBoundary(rendered.Body, streamtransport.MaxMatrixEventBodyBytes)
		continuationBody = rest
		rendered = format.RenderMarkdown(firstBody, true, true)
	}

	uiMessage := cc.buildCanonicalUIMessage(state, model, finishReason)
	topLevelExtra := map[string]any{
		matrixevents.BeeperAIKey:        uiMessage,
		"com.beeper.dont_render_edited": true,
		"m.mentions":                    map[string]any{},
	}

	sender := cc.senderForPortal()
	cc.UserLogin.QueueRemoteEvent(&CodexRemoteEdit{
		portal:        portal.PortalKey,
		sender:        sender,
		targetMessage: state.networkMessageID,
		timestamp:     time.Now(),
		preBuilt: &bridgev2.ConvertedEdit{
			ModifiedParts: []*bridgev2.ConvertedEditPart{{
				Type: event.EventMessage,
				Content: &event.MessageEventContent{
					MsgType:       event.MsgText,
					Body:          rendered.Body,
					Format:        rendered.Format,
					FormattedBody: rendered.FormattedBody,
				},
				Extra:         map[string]any{"m.mentions": map[string]any{}},
				TopLevelExtra: topLevelExtra,
			}},
		},
	})
	cc.loggerForContext(ctx).Debug().
		Str("initial_event_id", state.initialEventID.String()).
		Str("turn_id", state.turnID).
		Bool("has_thinking", state.reasoning.Len() > 0).
		Int("tool_calls", len(state.toolCalls)).
		Msg("Queued final assistant turn edit")

	for continuationBody != "" {
		var chunk string
		chunk, continuationBody = streamtransport.SplitAtMarkdownBoundary(continuationBody, streamtransport.MaxMatrixEventBodyBytes)
		cc.sendContinuationMessage(ctx, portal, chunk)
	}
}

func (cc *CodexClient) sendContinuationMessage(ctx context.Context, portal *bridgev2.Portal, body string) {
	if portal == nil || portal.MXID == "" {
		return
	}
	rendered := format.RenderMarkdown(body, true, true)
	raw := map[string]any{
		"msgtype":                 event.MsgText,
		"body":                    rendered.Body,
		"format":                  rendered.Format,
		"formatted_body":          rendered.FormattedBody,
		"com.beeper.continuation": true,
		"m.mentions":              map[string]any{},
	}
	sender := cc.senderForPortal()
	cc.UserLogin.QueueRemoteEvent(&CodexRemoteMessage{
		portal:    portal.PortalKey,
		id:        newMessageID(),
		sender:    sender,
		timestamp: time.Now(),
		preBuilt: &bridgev2.ConvertedMessage{
			Parts: []*bridgev2.ConvertedMessagePart{{
				ID:      networkid.PartID("0"),
				Type:    event.EventMessage,
				Content: &event.MessageEventContent{MsgType: event.MsgText, Body: body},
				Extra:   raw,
			}},
		},
	})
	cc.loggerForContext(ctx).Debug().Int("body_len", len(body)).Msg("Queued continuation message for oversized response")
}

// --- Message persistence ---

func (cc *CodexClient) saveAssistantMessage(ctx context.Context, portal *bridgev2.Portal, state *streamingState, model string, finishReason string) {
	if portal == nil || state == nil || !state.hasInitialMessageTarget() {
		return
	}
	log := cc.loggerForContext(ctx)

	var genFiles []GeneratedFileRef
	if len(state.generatedFiles) > 0 {
		genFiles = make([]GeneratedFileRef, 0, len(state.generatedFiles))
		for _, f := range state.generatedFiles {
			genFiles = append(genFiles, GeneratedFileRef{URL: f.URL, MimeType: f.MediaType})
		}
	}

	fullMeta := &MessageMetadata{
		Role:               "assistant",
		Body:               state.accumulated.String(),
		FinishReason:       finishReason,
		Model:              model,
		TurnID:             state.turnID,
		AgentID:            state.agentID,
		ToolCalls:          state.toolCalls,
		StartedAtMs:        state.startedAtMs,
		FirstTokenAtMs:     state.firstTokenAtMs,
		CompletedAtMs:      state.completedAtMs,
		HasToolCalls:       len(state.toolCalls) > 0,
		CanonicalSchema:    "ai-sdk-ui-message-v1",
		CanonicalUIMessage: cc.buildCanonicalUIMessage(state, model, finishReason),
		GeneratedFiles:     genFiles,
		ThinkingContent:    state.reasoning.String(),
		ThinkingTokenCount: len(strings.Fields(state.reasoning.String())),
		PromptTokens:       state.promptTokens,
		CompletionTokens:   state.completionTokens,
		ReasoningTokens:    state.reasoningTokens,
	}

	// If the message was sent via sendViaPortal, the DB row already exists -- update it.
	if state.networkMessageID != "" {
		receiver := portal.Receiver
		if receiver == "" && cc.UserLogin != nil {
			receiver = cc.UserLogin.ID
		}
		var existing *database.Message
		var err error
		if receiver != "" {
			existing, err = cc.UserLogin.Bridge.DB.Message.GetPartByID(ctx, receiver, state.networkMessageID, networkid.PartID("0"))
		}
		if existing == nil && state.initialEventID != "" {
			existing, err = cc.UserLogin.Bridge.DB.Message.GetPartByMXID(ctx, state.initialEventID)
		}
		if err == nil && existing != nil {
			existing.Metadata = fullMeta
			if err := cc.UserLogin.Bridge.DB.Message.Update(ctx, existing); err != nil {
				log.Warn().Err(err).Str("msg_id", string(existing.ID)).Msg("Failed to update assistant message metadata")
			} else {
				log.Debug().Str("msg_id", string(existing.ID)).Msg("Updated assistant message metadata")
			}
			return
		}
		log.Warn().
			Err(err).
			Stringer("mxid", state.initialEventID).
			Str("msg_id", string(state.networkMessageID)).
			Msg("Could not find existing DB row for update, falling back to insert")
	}
	if state.initialEventID == "" {
		return
	}

	assistantMsg := &database.Message{
		ID:        bridgeadapter.MatrixMessageID(state.initialEventID),
		Room:      portal.PortalKey,
		SenderID:  codexGhostID,
		MXID:      state.initialEventID,
		Timestamp: time.Now(),
		Metadata:  fullMeta,
	}
	if err := cc.UserLogin.Bridge.DB.Message.Insert(ctx, assistantMsg); err != nil {
		log.Warn().Err(err).Msg("Failed to save assistant message")
	} else {
		log.Debug().Str("msg_id", string(assistantMsg.ID)).Msg("Saved assistant message to database")
	}
}

// --- Tool approval timeline events ---

func (cc *CodexClient) sendToolCallApprovalEvent(
	ctx context.Context, portal *bridgev2.Portal, state *streamingState,
	toolCallID, toolName, approvalID string, expiresAtMs int64,
) {
	if portal == nil || portal.MXID == "" || state == nil || state.suppressSend {
		return
	}
	displayTitle := streamui.ToolDisplayTitle(toolName)
	toolType := string(matrixevents.ToolTypeProvider)
	if tt, ok := state.ui.UIToolTypeByToolCallID[toolCallID]; ok {
		toolType = string(tt)
	}
	toolCallData := map[string]any{
		"call_id":                toolCallID,
		"turn_id":                state.turnID,
		"tool_name":              toolName,
		"tool_type":              toolType,
		"status":                 string(matrixevents.ToolStatusApprovalRequired),
		"approval_id":            approvalID,
		"approval_expires_at_ms": expiresAtMs,
		"display": map[string]any{
			"title":     displayTitle,
			"collapsed": false,
		},
	}
	eventRaw := map[string]any{
		"body":                           fmt.Sprintf("Approval required for %s", displayTitle),
		"msgtype":                        event.MsgNotice,
		matrixevents.BeeperAIToolCallKey: toolCallData,
	}
	if state.initialEventID != "" {
		eventRaw["m.relates_to"] = map[string]any{
			"rel_type": matrixevents.RelReference,
			"event_id": state.initialEventID.String(),
		}
	}
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:    networkid.PartID("0"),
			Type:  matrixevents.ToolCallEventType,
			Extra: eventRaw,
		}},
	}
	eventID, _, err := cc.sendViaPortal(ctx, portal, converted, "")
	if err != nil {
		cc.loggerForContext(ctx).Warn().Err(err).
			Str("tool", toolName).Str("approval_id", approvalID).
			Msg("Failed to send tool call approval event")
		return
	}
	cc.loggerForContext(ctx).Debug().
		Stringer("event_id", eventID).
		Str("call_id", toolCallID).
		Str("tool", toolName).
		Str("approval_id", approvalID).
		Msg("Sent tool call approval_required timeline event")
}

func (cc *CodexClient) sendActionHintsApprovalEvent(
	ctx context.Context, portal *bridgev2.Portal, state *streamingState,
	toolCallID, toolName, approvalID string, expiresAtMs int64,
) {
	if portal == nil || portal.MXID == "" {
		return
	}

	var ownerMXID id.UserID
	if cc.UserLogin != nil {
		ownerMXID = cc.UserLogin.UserMXID
	}

	hints := bridgeadapter.BuildApprovalHints(bridgeadapter.ApprovalHintsParams{
		ApprovalID:  approvalID,
		ToolCallID:  toolCallID,
		ToolName:    toolName,
		OwnerMXID:   ownerMXID,
		ExpiresAtMs: expiresAtMs,
	})

	body := fmt.Sprintf("Allow %s tool?", toolName)
	uiMessage := map[string]any{
		"id":   "approval:" + approvalID,
		"role": "assistant",
		"parts": []map[string]any{
			{
				"type":       "action-hints",
				"toolCallId": toolCallID,
				"toolName":   toolName,
			},
		},
	}
	if state != nil && state.turnID != "" {
		uiMessage["metadata"] = map[string]any{"turn_id": state.turnID}
	}

	eventRaw := map[string]any{
		"msgtype":                         event.MsgNotice,
		"body":                            body,
		matrixevents.BeeperAIKey:          uiMessage,
		matrixevents.BeeperActionHintsKey: hints,
		"m.mentions":                      map[string]any{},
	}

	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:    networkid.PartID("0"),
			Type:  event.EventMessage,
			Extra: eventRaw,
		}},
	}
	if evtID, msgID, err := cc.sendViaPortal(ctx, portal, converted, ""); err == nil && evtID != "" {
		cc.approvals.SetData(approvalID, func(data any) any {
			if d, ok := data.(*pendingToolApprovalDataCodex); ok {
				d.ApprovalEventID = evtID
				d.ApprovalNetworkMsgID = msgID
			}
			return data
		})
	}
}
