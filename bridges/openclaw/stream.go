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

func applyOpenClawStreamPartTimestamp(state *openClawStreamState, ts time.Time) {
	if state == nil || ts.IsZero() {
		return
	}
	if state.messageTS.IsZero() || ts.Before(state.messageTS) {
		state.messageTS = ts
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

	oc.streamHost.Lock()
	state := oc.ensureStreamStateLocked(portal, turnID, agentID, sessionKey)
	oc.applyStreamPartStateLocked(state, part)
	turn := state.turn
	needsTurn := turn == nil
	oc.streamHost.Unlock()

	if needsTurn {
		turn = oc.newSDKStreamTurn(ctx, portal, state)
		oc.streamHost.Lock()
		if state.turn == nil {
			state.turn = turn
		} else {
			turn = state.turn
		}
		oc.streamHost.Unlock()
	}

	if oc.IsStreamShuttingDown() {
		return
	}
	if turn == nil {
		return
	}
	bridgesdk.ApplyStreamPart(turn, part, bridgesdk.PartApplyOptions{
		HandleTerminalEvents: true,
		DefaultFinishReason:  "stop",
	})
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
			state.stream.SetFinishReason(strings.TrimSpace(finishReason))
		}
		if state.stream.CompletedAtMs() == 0 {
			state.stream.SetCompletedAtMs(time.Now().UnixMilli())
		}
		meta := oc.buildStreamDBMetadata(state)
		oc.streamHost.DeleteIfMatch(state.turnID, state)
		return meta
	}))
	return turn
}

func (oc *OpenClawClient) computeVisibleDelta(turnID, text string) string {
	turnID = strings.TrimSpace(turnID)
	text = strings.TrimSpace(text)
	if turnID == "" {
		return text
	}

	oc.streamHost.Lock()
	defer oc.streamHost.Unlock()
	state := oc.streamHost.GetLocked(turnID)
	if state == nil {
		state = &openClawStreamState{turnID: turnID}
		oc.streamHost.SetLocked(turnID, state)
	}
	if text == state.stream.LastVisibleText() {
		return ""
	}
	prev := state.stream.LastVisibleText()
	state.stream.SetLastVisibleText(text)
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
	return oc.streamHost.IsActive(turnID)
}

func (oc *OpenClawClient) ensureStreamStateLocked(portal *bridgev2.Portal, turnID, agentID, sessionKey string) *openClawStreamState {
	state := oc.streamHost.GetLocked(turnID)
	if state == nil {
		state = &openClawStreamState{
			portal:     portal,
			turnID:     turnID,
			agentID:    agentID,
			sessionKey: sessionKey,
			role:       "assistant",
		}
		oc.streamHost.SetLocked(turnID, state)
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
	partTS := openClawStreamPartTimestamp(part)
	applyOpenClawStreamPartTimestamp(state, partTS)
	state.stream.ApplyPart(part, partTS)
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
		state.stream.SetFinishReason(value)
	}
	if value := maputil.StringArg(metadata, "error_text"); value != "" {
		state.stream.SetErrorText(value)
	}
	if timing, _ := metadata["timing"].(map[string]any); len(timing) > 0 {
		if value, ok := maputil.NumberArg(timing, "started_at"); ok {
			state.stream.SetStartedAtMs(int64(value))
		}
		if value, ok := maputil.NumberArg(timing, "first_token_at"); ok {
			state.stream.SetFirstTokenAtMs(int64(value))
		}
		if value, ok := maputil.NumberArg(timing, "completed_at"); ok {
			state.stream.SetCompletedAtMs(int64(value))
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
		FinishReason:     state.stream.FinishReason(),
		CompletionID:     state.runID,
		PromptTokens:     state.promptTokens,
		CompletionTokens: state.completionTokens,
		ReasoningTokens:  state.reasoningTokens,
		TotalTokens:      state.totalTokens,
		StartedAtMs:      state.stream.StartedAtMs(),
		FirstTokenAtMs:   state.stream.FirstTokenAtMs(),
		CompletedAtMs:    state.stream.CompletedAtMs(),
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
	body := strings.TrimSpace(state.stream.LastVisibleText())
	if body == "" {
		body = strings.TrimSpace(state.stream.VisibleText())
	}
	if body == "" {
		body = strings.TrimSpace(state.stream.AccumulatedText())
	}
	uiMessage := oc.currentUIMessage(state)
	snapshot := bridgesdk.BuildTurnSnapshot(uiMessage, bridgesdk.TurnDataBuildOptions{
		ID:   state.turnID,
		Role: stringutil.TrimDefault(state.role, "assistant"),
		Text: body,
		Metadata: map[string]any{
			"turn_id":           state.turnID,
			"agent_id":          state.agentID,
			"finish_reason":     state.stream.FinishReason(),
			"prompt_tokens":     state.promptTokens,
			"completion_tokens": state.completionTokens,
			"reasoning_tokens":  state.reasoningTokens,
			"started_at_ms":     state.stream.StartedAtMs(),
			"completed_at_ms":   state.stream.CompletedAtMs(),
		},
	}, "openclaw")
	return &MessageMetadata{
		BaseMessageMetadata: agentremote.BaseMessageMetadata{
			Role:              stringutil.TrimDefault(state.role, "assistant"),
			Body:              snapshot.Body,
			TurnID:            state.turnID,
			AgentID:           state.agentID,
			FinishReason:      state.stream.FinishReason(),
			PromptTokens:      state.promptTokens,
			CompletionTokens:  state.completionTokens,
			ReasoningTokens:   state.reasoningTokens,
			CanonicalTurnData: snapshot.TurnData.ToMap(),
			ThinkingContent:   snapshot.ThinkingContent,
			ToolCalls:         snapshot.ToolCalls,
			GeneratedFiles:    snapshot.GeneratedFiles,
			StartedAtMs:       state.stream.StartedAtMs(),
			CompletedAtMs:     state.stream.CompletedAtMs(),
		},
		SessionID:      state.sessionID,
		SessionKey:     state.sessionKey,
		RunID:          state.runID,
		ErrorText:      state.stream.ErrorText(),
		TotalTokens:    state.totalTokens,
		FirstTokenAtMs: state.stream.FirstTokenAtMs(),
	}
}
