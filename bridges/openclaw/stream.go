package openclaw

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/connector/msgconv"
	"github.com/beeper/ai-bridge/pkg/matrixevents"
	"github.com/beeper/ai-bridge/pkg/shared/maputil"
	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
	"github.com/beeper/ai-bridge/pkg/shared/streamui"
)

func (oc *OpenClawClient) EmitStreamPart(ctx context.Context, portal *bridgev2.Portal, turnID, agentID, sessionKey string, part map[string]any) {
	if oc == nil || portal == nil || portal.MXID == "" || strings.TrimSpace(turnID) == "" || part == nil {
		return
	}
	if oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.Bot == nil {
		return
	}

	turnID = strings.TrimSpace(turnID)
	agentID = stringsTrimDefault(agentID, "gateway")
	sessionKey = strings.TrimSpace(sessionKey)

	oc.streamMu.Lock()
	state := oc.ensureStreamStateLocked(portal, turnID, agentID, sessionKey)
	if metadata, _ := part["messageMetadata"].(map[string]any); len(metadata) > 0 {
		oc.applyStreamMessageMetadata(state, metadata)
	}
	partType := strings.TrimSpace(stringValue(part["type"]))
	if state.startedAtMs == 0 && partType == "start" {
		state.startedAtMs = time.Now().UnixMilli()
	}
	switch partType {
	case "text-delta":
		if delta := stringValue(part["delta"]); delta != "" {
			state.visible.WriteString(delta)
			state.accumulated.WriteString(delta)
			if state.firstTokenAtMs == 0 {
				state.firstTokenAtMs = time.Now().UnixMilli()
			}
		}
	case "reasoning-delta":
		if delta := stringValue(part["delta"]); delta != "" {
			state.accumulated.WriteString(delta)
			if state.firstTokenAtMs == 0 {
				state.firstTokenAtMs = time.Now().UnixMilli()
			}
		}
	case "error":
		if errText := strings.TrimSpace(stringValue(part["errorText"])); errText != "" {
			state.errorText = errText
		}
	case "abort":
		state.finishReason = stringsTrimDefault(stringValue(part["reason"]), "aborted")
	case "finish":
		if state.completedAtMs == 0 {
			state.completedAtMs = time.Now().UnixMilli()
		}
	}
	streamui.ApplyChunk(&state.ui, part)
	needPlaceholder := state.initialEventID == ""
	oc.streamMu.Unlock()

	if needPlaceholder {
		oc.ensureStreamPlaceholder(portal, turnID, agentID)
	}

	oc.streamMu.Lock()
	state = oc.ensureStreamStateLocked(portal, turnID, agentID, sessionKey)
	session := oc.streamSessions[turnID]
	if session == nil {
		session = streamtransport.NewStreamSession(streamtransport.StreamSessionParams{
			TurnID:  turnID,
			AgentID: state.agentID,
			GetTargetEventID: func() string {
				oc.streamMu.Lock()
				defer oc.streamMu.Unlock()
				if current := oc.streamStates[turnID]; current != nil {
					return current.targetEventID
				}
				return ""
			},
			GetRoomID: func() id.RoomID {
				return portal.MXID
			},
			GetSuppressSend: func() bool { return false },
			NextSeq: func() int {
				oc.streamMu.Lock()
				defer oc.streamMu.Unlock()
				if current := oc.streamStates[turnID]; current != nil {
					current.sequenceNum++
					return current.sequenceNum
				}
				return 0
			},
			RuntimeFallbackFlag: &oc.streamFallbackToDebounced,
			GetEphemeralSender: func(callCtx context.Context) (bridgev2.EphemeralSendingMatrixAPI, bool) {
				ephemeralSender, ok := any(oc.UserLogin.Bridge.Bot).(bridgev2.EphemeralSendingMatrixAPI)
				return ephemeralSender, ok
			},
			SendDebouncedEdit: func(callCtx context.Context, force bool) error {
				oc.streamMu.Lock()
				current := oc.streamStates[turnID]
				oc.streamMu.Unlock()
				return oc.queueDebouncedStreamEdit(callCtx, portal, current, force)
			},
			Logger: oc.Log(),
		})
		oc.streamSessions[turnID] = session
	}
	oc.streamMu.Unlock()
	session.EmitPart(ctx, part)
}

func (oc *OpenClawClient) FinishStream(turnID, finishReason string) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return
	}

	oc.streamMu.Lock()
	session := oc.streamSessions[turnID]
	state := oc.streamStates[turnID]
	delete(oc.streamSessions, turnID)
	if state != nil {
		if state.finishReason == "" {
			state.finishReason = strings.TrimSpace(finishReason)
		}
		if state.completedAtMs == 0 {
			state.completedAtMs = time.Now().UnixMilli()
		}
	}
	oc.streamMu.Unlock()

	if state != nil && state.portal != nil {
		ctx := oc.BackgroundContext(context.Background())
		oc.queueFinalStreamEdit(ctx, state.portal, state)
		oc.persistStreamDBMetadata(ctx, state.portal, state, oc.buildStreamDBMetadata(state))
	}

	oc.streamMu.Lock()
	delete(oc.streamStates, turnID)
	oc.streamMu.Unlock()

	if session != nil {
		session.End(oc.BackgroundContext(context.Background()), streamtransport.EndReasonFinish)
	}
}

func (oc *OpenClawClient) computeVisibleDelta(turnID, text string) string {
	turnID = strings.TrimSpace(turnID)
	text = strings.TrimSpace(text)
	if turnID == "" {
		return text
	}

	oc.streamMu.Lock()
	defer oc.streamMu.Unlock()
	state := oc.streamStates[turnID]
	if state == nil {
		state = &openClawStreamState{turnID: turnID}
		state.ui.TurnID = turnID
		oc.streamStates[turnID] = state
	}
	if text == state.lastVisibleText {
		return ""
	}
	prev := state.lastVisibleText
	state.lastVisibleText = text
	if prev == "" {
		return text
	}
	if strings.HasPrefix(text, prev) {
		return text[len(prev):]
	}
	return text
}

func (oc *OpenClawClient) ensureStreamStateLocked(portal *bridgev2.Portal, turnID, agentID, sessionKey string) *openClawStreamState {
	state := oc.streamStates[turnID]
	if state == nil {
		state = &openClawStreamState{
			portal:      portal,
			turnID:      turnID,
			agentID:     agentID,
			sessionKey:  sessionKey,
			role:        "assistant",
			startedAtMs: time.Now().UnixMilli(),
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
	if state.sessionKey == "" {
		state.sessionKey = sessionKey
	}
	if state.ui.TurnID == "" {
		state.ui.TurnID = turnID
	}
	if state.role == "" {
		state.role = "assistant"
	}
	return state
}

func (oc *OpenClawClient) ensureStreamPlaceholder(portal *bridgev2.Portal, turnID, agentID string) {
	oc.streamMu.Lock()
	state := oc.streamStates[turnID]
	if state == nil || state.initialEventID != "" {
		oc.streamMu.Unlock()
		return
	}
	uiMessage := oc.currentCanonicalUIMessage(state)
	startedAtMs := state.startedAtMs
	runID := state.runID
	sessionID := state.sessionID
	sessionKey := state.sessionKey
	oc.streamMu.Unlock()

	msgID := newOpenClawMessageID()
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:      networkid.PartID("0"),
			Type:    event.EventMessage,
			Content: &event.MessageEventContent{MsgType: event.MsgText, Body: "..."},
			Extra: map[string]any{
				"msgtype":                event.MsgText,
				"body":                   "...",
				"m.mentions":             map[string]any{},
				matrixevents.BeeperAIKey: uiMessage,
			},
			DBMetadata: &MessageMetadata{
				Role:               "assistant",
				Body:               "...",
				RunID:              runID,
				TurnID:             turnID,
				AgentID:            agentID,
				SessionID:          sessionID,
				SessionKey:         sessionKey,
				CanonicalSchema:    "ai-sdk-ui-message-v1",
				CanonicalUIMessage: uiMessage,
				StartedAtMs:        startedAtMs,
			},
		}},
	}
	result := oc.UserLogin.QueueRemoteEvent(&OpenClawRemoteMessage{
		portal:    portal.PortalKey,
		id:        msgID,
		sender:    oc.senderForAgent(agentID, false),
		timestamp: time.Now(),
		preBuilt:  converted,
	})

	if !result.Success || result.EventID == "" {
		return
	}

	oc.streamMu.Lock()
	defer oc.streamMu.Unlock()
	state = oc.streamStates[turnID]
	if state == nil || state.initialEventID != "" {
		return
	}
	state.initialEventID = result.EventID
	state.networkMessageID = msgID
	state.targetEventID = result.EventID.String()
}

func (oc *OpenClawClient) applyStreamMessageMetadata(state *openClawStreamState, metadata map[string]any) {
	if state == nil || len(metadata) == 0 {
		return
	}
	if value := maputil.StringArg(metadata, "role"); value != "" {
		state.role = value
	}
	if value := maputil.StringArg(metadata, "session_id"); value != "" {
		state.sessionID = value
	}
	if value := maputil.StringArg(metadata, "session_key"); value != "" {
		state.sessionKey = value
	}
	if value := maputil.StringArg(metadata, "completion_id"); value != "" {
		state.runID = value
	}
	if value := maputil.StringArg(metadata, "agent_id"); value != "" {
		state.agentID = value
	}
	if value := maputil.StringArg(metadata, "finish_reason"); value != "" {
		state.finishReason = value
	}
	if value := maputil.StringArg(metadata, "error_text"); value != "" {
		state.errorText = value
	}
	if timing, _ := metadata["timing"].(map[string]any); len(timing) > 0 {
		if value, ok := maputil.NumberArg(timing, "started_at"); ok {
			state.startedAtMs = int64(value)
		}
		if value, ok := maputil.NumberArg(timing, "first_token_at"); ok {
			state.firstTokenAtMs = int64(value)
		}
		if value, ok := maputil.NumberArg(timing, "completed_at"); ok {
			state.completedAtMs = int64(value)
		}
	}
	if usage, _ := metadata["usage"].(map[string]any); len(usage) > 0 {
		usage = normalizeOpenClawUsage(usage)
		if value, ok := maputil.NumberArg(usage, "prompt_tokens"); ok {
			state.promptTokens = int64(value)
		}
		if value, ok := maputil.NumberArg(usage, "completion_tokens"); ok {
			state.completionTokens = int64(value)
		}
		if value, ok := maputil.NumberArg(usage, "reasoning_tokens"); ok {
			state.reasoningTokens = int64(value)
		}
		if value, ok := maputil.NumberArg(usage, "total_tokens"); ok {
			state.totalTokens = int64(value)
		}
	}
}

func (oc *OpenClawClient) currentCanonicalUIMessage(state *openClawStreamState) map[string]any {
	if state == nil {
		return nil
	}
	uiMessage := streamui.SnapshotCanonicalUIMessage(&state.ui)
	update := msgconv.BuildUIMessageMetadata(msgconv.UIMessageMetadataParams{
		TurnID:           state.turnID,
		AgentID:          state.agentID,
		FinishReason:     state.finishReason,
		CompletionID:     state.runID,
		PromptTokens:     state.promptTokens,
		CompletionTokens: state.completionTokens,
		ReasoningTokens:  state.reasoningTokens,
		TotalTokens:      state.totalTokens,
		StartedAtMs:      state.startedAtMs,
		FirstTokenAtMs:   state.firstTokenAtMs,
		CompletedAtMs:    state.completedAtMs,
		IncludeUsage:     true,
	})
	if len(uiMessage) == 0 {
		return msgconv.BuildUIMessage(msgconv.UIMessageParams{
			TurnID:   state.turnID,
			Role:     stringsTrimDefault(state.role, "assistant"),
			Metadata: update,
		})
	}
	metadata, _ := uiMessage["metadata"].(map[string]any)
	uiMessage["metadata"] = msgconv.MergeUIMessageMetadata(metadata, update)
	return uiMessage
}

func (oc *OpenClawClient) buildStreamDBMetadata(state *openClawStreamState) *MessageMetadata {
	if state == nil {
		return nil
	}
	body := strings.TrimSpace(state.lastVisibleText)
	if body == "" {
		body = strings.TrimSpace(state.visible.String())
	}
	if body == "" {
		body = strings.TrimSpace(state.accumulated.String())
	}
	return &MessageMetadata{
		Role:               stringsTrimDefault(state.role, "assistant"),
		Body:               body,
		SessionID:          state.sessionID,
		SessionKey:         state.sessionKey,
		RunID:              state.runID,
		TurnID:             state.turnID,
		AgentID:            state.agentID,
		FinishReason:       state.finishReason,
		ErrorText:          state.errorText,
		PromptTokens:       state.promptTokens,
		CompletionTokens:   state.completionTokens,
		ReasoningTokens:    state.reasoningTokens,
		TotalTokens:        state.totalTokens,
		CanonicalSchema:    "ai-sdk-ui-message-v1",
		CanonicalUIMessage: oc.currentCanonicalUIMessage(state),
		StartedAtMs:        state.startedAtMs,
		FirstTokenAtMs:     state.firstTokenAtMs,
		CompletedAtMs:      state.completedAtMs,
	}
}

func (oc *OpenClawClient) persistStreamDBMetadata(ctx context.Context, portal *bridgev2.Portal, state *openClawStreamState, meta *MessageMetadata) {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || portal == nil || state == nil || meta == nil {
		return
	}
	receiver := portal.Receiver
	if receiver == "" {
		receiver = oc.UserLogin.ID
	}
	var existing *database.Message
	var err error
	if state.networkMessageID != "" {
		existing, err = oc.UserLogin.Bridge.DB.Message.GetPartByID(ctx, receiver, state.networkMessageID, networkid.PartID("0"))
	}
	if existing == nil && state.initialEventID != "" {
		existing, err = oc.UserLogin.Bridge.DB.Message.GetPartByMXID(ctx, state.initialEventID)
	}
	if err != nil {
		oc.Log().Warn().
			Err(err).
			Str("receiver", string(receiver)).
			Str("network_message_id", string(state.networkMessageID)).
			Stringer("initial_event_id", state.initialEventID).
			Msg("Failed to load OpenClaw stream message for metadata update")
		return
	}
	if existing == nil {
		return
	}
	existing.Metadata = meta
	if err := oc.UserLogin.Bridge.DB.Message.Update(ctx, existing); err != nil {
		oc.Log().Warn().
			Err(err).
			Str("receiver", string(receiver)).
			Str("network_message_id", string(state.networkMessageID)).
			Stringer("initial_event_id", state.initialEventID).
			Msg("Failed to persist OpenClaw stream metadata")
	}
}

func (oc *OpenClawClient) queueDebouncedStreamEdit(ctx context.Context, portal *bridgev2.Portal, state *openClawStreamState, force bool) error {
	if oc == nil || portal == nil || portal.MXID == "" || state == nil || state.networkMessageID == "" {
		return nil
	}
	visibleBody := strings.TrimSpace(state.lastVisibleText)
	if visibleBody == "" {
		visibleBody = strings.TrimSpace(state.visible.String())
	}
	fallbackBody := strings.TrimSpace(state.accumulated.String())
	content := streamtransport.BuildDebouncedEditContent(streamtransport.DebouncedEditParams{
		PortalMXID:   portal.MXID.String(),
		Force:        force,
		SuppressSend: false,
		VisibleBody:  visibleBody,
		FallbackBody: fallbackBody,
	})
	if content == nil {
		return nil
	}
	oc.UserLogin.QueueRemoteEvent(&OpenClawRemoteEdit{
		portal:        portal.PortalKey,
		sender:        oc.senderForAgent(state.agentID, false),
		targetMessage: state.networkMessageID,
		timestamp:     time.Now(),
		preBuilt: &bridgev2.ConvertedEdit{
			ModifiedParts: []*bridgev2.ConvertedEditPart{{
				Type: event.EventMessage,
				Content: &event.MessageEventContent{
					MsgType:       event.MsgText,
					Body:          content.Body,
					Format:        content.Format,
					FormattedBody: content.FormattedBody,
				},
				Extra: map[string]any{"m.mentions": map[string]any{}},
				TopLevelExtra: map[string]any{
					matrixevents.BeeperAIKey:        oc.currentCanonicalUIMessage(state),
					"com.beeper.dont_render_edited": true,
					"m.mentions":                    map[string]any{},
				},
			}},
		},
	})
	return nil
}

func (oc *OpenClawClient) queueFinalStreamEdit(ctx context.Context, portal *bridgev2.Portal, state *openClawStreamState) {
	if oc == nil || portal == nil || portal.MXID == "" || state == nil || state.networkMessageID == "" {
		return
	}
	body := strings.TrimSpace(state.lastVisibleText)
	if body == "" {
		body = strings.TrimSpace(state.visible.String())
	}
	if body == "" {
		body = strings.TrimSpace(state.accumulated.String())
	}
	if body == "" {
		body = "..."
	}
	rendered := format.RenderMarkdown(body, true, true)
	oc.UserLogin.QueueRemoteEvent(&OpenClawRemoteEdit{
		portal:        portal.PortalKey,
		sender:        oc.senderForAgent(state.agentID, false),
		targetMessage: state.networkMessageID,
		timestamp:     time.Now(),
		preBuilt: &bridgev2.ConvertedEdit{
			ModifiedParts: []*bridgev2.ConvertedEditPart{{
				Type: event.EventMessage,
				Content: &event.MessageEventContent{
					MsgType:       event.MsgText,
					Body:          body,
					Format:        rendered.Format,
					FormattedBody: rendered.FormattedBody,
				},
				Extra: map[string]any{"m.mentions": map[string]any{}},
				TopLevelExtra: map[string]any{
					matrixevents.BeeperAIKey:        oc.currentCanonicalUIMessage(state),
					"com.beeper.dont_render_edited": true,
					"m.mentions":                    map[string]any{},
				},
			}},
		},
	})
}
