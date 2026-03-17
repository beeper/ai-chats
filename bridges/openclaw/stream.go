package openclaw

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/bridges/ai/msgconv"
	"github.com/beeper/agentremote/pkg/shared/maputil"
	"github.com/beeper/agentremote/pkg/shared/streamui"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

func openClawStreamPartTimestamp(part map[string]any) time.Time {
	if len(part) == 0 {
		return time.Time{}
	}
	if value, ok := maputil.NumberArg(part, "timestamp"); ok && value > 0 {
		return time.UnixMilli(int64(value))
	}
	return time.Time{}
}

func applyOpenClawStreamPartTimestamp(state *openClawStreamState, partType string, ts time.Time) {
	if state == nil || ts.IsZero() {
		return
	}
	tsMillis := ts.UnixMilli()
	if state.messageTS.IsZero() || ts.Before(state.messageTS) {
		state.messageTS = ts
	}
	switch partType {
	case "start":
		if state.startedAtMs == 0 || tsMillis < state.startedAtMs {
			state.startedAtMs = tsMillis
		}
	case "text-delta", "reasoning-delta":
		if state.startedAtMs == 0 || tsMillis < state.startedAtMs {
			state.startedAtMs = tsMillis
		}
		if state.firstTokenAtMs == 0 || tsMillis < state.firstTokenAtMs {
			state.firstTokenAtMs = tsMillis
		}
	case "abort", "error", "finish":
		if state.completedAtMs == 0 || tsMillis > state.completedAtMs {
			state.completedAtMs = tsMillis
		}
	}
}

func (oc *OpenClawClient) EmitStreamPart(ctx context.Context, portal *bridgev2.Portal, turnID, agentID, sessionKey string, part map[string]any) {
	if oc == nil || portal == nil || portal.MXID == "" || strings.TrimSpace(turnID) == "" || part == nil {
		return
	}
	if oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.Bot == nil {
		return
	}
	if oc.IsStreamShuttingDown() {
		return
	}

	turnID = strings.TrimSpace(turnID)
	agentID = stringutil.TrimDefault(agentID, "gateway")
	sessionKey = strings.TrimSpace(sessionKey)

	oc.StreamMu.Lock()
	state := oc.ensureStreamStateLocked(portal, turnID, agentID, sessionKey)
	oc.applyStreamPartStateLocked(state, part)
	turn := state.turn
	needsTurn := turn == nil
	partType := strings.TrimSpace(stringValue(part["type"]))
	oc.StreamMu.Unlock()

	if needsTurn {
		turn = oc.newSDKStreamTurn(ctx, portal, state)
		oc.StreamMu.Lock()
		if state.turn == nil {
			state.turn = turn
		} else {
			turn = state.turn
		}
		oc.StreamMu.Unlock()
	}

	if oc.IsStreamShuttingDown() {
		return
	}
	if turn == nil {
		return
	}
	bridgesdk.ApplyStreamPart(turn, part, bridgesdk.PartApplyOptions{})
	if partType == "finish" {
		oc.completeStreamTurn(turnID, state, turn)
	}
}

func (oc *OpenClawClient) completeStreamTurn(turnID string, state *openClawStreamState, turn *bridgesdk.Turn) {
	if strings.TrimSpace(turnID) == "" || state == nil || turn == nil {
		return
	}
	switch strings.TrimSpace(state.finishReason) {
	case "abort", "aborted":
		turn.Abort(stringutil.TrimDefault(state.finishReason, "aborted"))
	case "error":
		turn.EndWithError(stringutil.TrimDefault(state.errorText, "OpenClaw stream failed"))
	default:
		turn.End(stringutil.TrimDefault(state.finishReason, "stop"))
	}
	oc.StreamMu.Lock()
	if oc.streamStates[turnID] == state {
		delete(oc.streamStates, turnID)
	}
	oc.StreamMu.Unlock()
}

func (oc *OpenClawClient) newSDKStreamTurn(ctx context.Context, portal *bridgev2.Portal, state *openClawStreamState) *bridgesdk.Turn {
	if oc == nil || portal == nil || state == nil || oc.connector == nil || oc.connector.sdkConfig == nil {
		return nil
	}
	profile := oc.resolveAgentProfile(ctx, state.agentID, state.sessionKey, nil, nil)
	state.agentID = stringutil.TrimDefault(profile.AgentID, state.agentID)
	state.agentID = stringutil.TrimDefault(state.agentID, "gateway")
	agent := oc.sdkAgentForProfile(profile)
	sender := oc.senderForAgent(state.agentID, false)
	conv := bridgesdk.NewConversation(ctx, oc.UserLogin, portal, sender, oc.connector.sdkConfig, oc)
	_ = conv.EnsureRoomAgent(ctx, agent)
	turn := conv.StartTurn(ctx, agent, nil)
	turn.SetID(state.turnID)
	turn.SetSender(sender)
	turn.SetFinalMetadataProvider(bridgesdk.FinalMetadataProviderFunc(func(_ *bridgesdk.Turn, finishReason string) any {
		if strings.TrimSpace(finishReason) != "" {
			state.finishReason = strings.TrimSpace(finishReason)
		}
		if state.completedAtMs == 0 {
			state.completedAtMs = time.Now().UnixMilli()
		}
		return oc.buildStreamDBMetadata(state)
	}))
	return turn
}

func (oc *OpenClawClient) computeVisibleDelta(turnID, text string) string {
	turnID = strings.TrimSpace(turnID)
	text = strings.TrimSpace(text)
	if turnID == "" {
		return text
	}

	oc.StreamMu.Lock()
	defer oc.StreamMu.Unlock()
	state := oc.streamStates[turnID]
	if state == nil {
		state = &openClawStreamState{turnID: turnID}
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

func (oc *OpenClawClient) isStreamActive(turnID string) bool {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return false
	}
	oc.StreamMu.Lock()
	defer oc.StreamMu.Unlock()
	_, ok := oc.streamStates[turnID]
	return ok
}

func (oc *OpenClawClient) ensureStreamStateLocked(portal *bridgev2.Portal, turnID, agentID, sessionKey string) *openClawStreamState {
	state := oc.streamStates[turnID]
	if state == nil {
		state = &openClawStreamState{
			portal:     portal,
			turnID:     turnID,
			agentID:    agentID,
			sessionKey: sessionKey,
			role:       "assistant",
		}
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
	if state.role == "" {
		state.role = "assistant"
	}
	return state
}

func (oc *OpenClawClient) applyStreamPartStateLocked(state *openClawStreamState, part map[string]any) {
	if state == nil || len(part) == 0 {
		return
	}
	if metadata, _ := part["messageMetadata"].(map[string]any); len(metadata) > 0 {
		oc.applyStreamMessageMetadata(state, metadata)
	}
	partType := strings.TrimSpace(stringValue(part["type"]))
	partTS := openClawStreamPartTimestamp(part)
	applyOpenClawStreamPartTimestamp(state, partType, partTS)
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
		state.finishReason = stringutil.TrimDefault(stringValue(part["reason"]), "aborted")
	case "finish":
		if finishReason := strings.TrimSpace(stringValue(part["finishReason"])); finishReason != "" {
			state.finishReason = finishReason
		}
		if errText := strings.TrimSpace(stringValue(part["errorText"])); errText != "" {
			state.errorText = errText
		}
		if state.completedAtMs == 0 {
			state.completedAtMs = time.Now().UnixMilli()
		}
	}
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

func (oc *OpenClawClient) currentUIMessage(state *openClawStreamState) map[string]any {
	if state == nil {
		return nil
	}
	uiState := &streamui.UIState{TurnID: state.turnID}
	uiState.InitMaps()
	if state.turn != nil && state.turn.UIState() != nil {
		uiState = state.turn.UIState()
	}
	uiMessage := streamui.SnapshotUIMessage(uiState)
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
			Role:     stringutil.TrimDefault(state.role, "assistant"),
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
	uiMessage := oc.currentUIMessage(state)
	snapshot := bridgesdk.BuildTurnSnapshot(uiMessage, bridgesdk.TurnDataBuildOptions{
		ID:   state.turnID,
		Role: stringutil.TrimDefault(state.role, "assistant"),
		Text: body,
		Metadata: map[string]any{
			"turn_id":           state.turnID,
			"agent_id":          state.agentID,
			"finish_reason":     state.finishReason,
			"prompt_tokens":     state.promptTokens,
			"completion_tokens": state.completionTokens,
			"reasoning_tokens":  state.reasoningTokens,
			"started_at_ms":     state.startedAtMs,
			"completed_at_ms":   state.completedAtMs,
		},
	}, "openclaw")
	return &MessageMetadata{
		BaseMessageMetadata: agentremote.BaseMessageMetadata{
			Role:              stringutil.TrimDefault(state.role, "assistant"),
			Body:              snapshot.Body,
			TurnID:            state.turnID,
			AgentID:           state.agentID,
			FinishReason:      state.finishReason,
			PromptTokens:      state.promptTokens,
			CompletionTokens:  state.completionTokens,
			ReasoningTokens:   state.reasoningTokens,
			CanonicalTurnData: snapshot.TurnData.ToMap(),
			ThinkingContent:   snapshot.ThinkingContent,
			ToolCalls:         snapshot.ToolCalls,
			GeneratedFiles:    snapshot.GeneratedFiles,
			StartedAtMs:       state.startedAtMs,
			CompletedAtMs:     state.completedAtMs,
		},
		SessionID:      state.sessionID,
		SessionKey:     state.sessionKey,
		RunID:          state.runID,
		ErrorText:      state.errorText,
		TotalTokens:    state.totalTokens,
		FirstTokenAtMs: state.firstTokenAtMs,
	}
}
