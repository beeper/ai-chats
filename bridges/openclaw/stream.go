package openclaw

import (
	"context"
	"strings"
	"sync"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/shared/maputil"
	"github.com/beeper/agentremote/pkg/shared/streamui"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/sdk"
)

var openClawNewSDKStreamTurn = (*OpenClawClient).newSDKStreamTurn

type openClawStreamTurnGate struct {
	mu       sync.Mutex
	cond     *sync.Cond
	creating bool
}

func newOpenClawStreamTurnGate() *openClawStreamTurnGate {
	gate := &openClawStreamTurnGate{}
	gate.cond = sync.NewCond(&gate.mu)
	return gate
}

var openClawStreamTurnGates sync.Map

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
	oc.streamHost.Unlock()

	turn := oc.ensureSDKStreamTurn(ctx, portal, state)

	if oc.IsStreamShuttingDown() {
		return
	}
	if turn == nil {
		return
	}
	sdk.ApplyStreamPart(turn, part, sdk.PartApplyOptions{
		HandleTerminalEvents: true,
		DefaultFinishReason:  "stop",
	})
}

func (oc *OpenClawClient) ensureSDKStreamTurn(ctx context.Context, portal *bridgev2.Portal, state *openClawStreamState) *sdk.Turn {
	if oc == nil || state == nil {
		return nil
	}

	gateAny, _ := openClawStreamTurnGates.LoadOrStore(state, newOpenClawStreamTurnGate())
	gate := gateAny.(*openClawStreamTurnGate)

	gate.mu.Lock()
	for state.turn == nil && gate.creating {
		gate.cond.Wait()
	}
	if state.turn != nil {
		turn := state.turn
		gate.mu.Unlock()
		return turn
	}
	gate.creating = true
	gate.mu.Unlock()

	turn := openClawNewSDKStreamTurn(oc, ctx, portal, state)

	gate.mu.Lock()
	if state.turn == nil {
		state.turn = turn
	} else {
		turn = state.turn
	}
	gate.creating = false
	gate.cond.Broadcast()
	gate.mu.Unlock()
	openClawStreamTurnGates.Delete(state)
	return turn
}

func (oc *OpenClawClient) newSDKStreamTurn(ctx context.Context, portal *bridgev2.Portal, state *openClawStreamState) *sdk.Turn {
	if oc == nil || portal == nil || state == nil || oc.connector == nil || oc.connector.sdkConfig == nil {
		return nil
	}
	profile := oc.resolveAgentProfile(ctx, state.agentID, state.sessionKey, nil, nil)
	state.agentID = stringutil.TrimDefault(profile.AgentID, state.agentID)
	state.agentID = stringutil.TrimDefault(state.agentID, "gateway")
	agent := oc.sdkAgentForProfile(profile)
	sender := oc.senderForAgent(state.agentID, false)
	conv := sdk.NewConversation(ctx, oc.UserLogin, portal, sender, oc.connector.sdkConfig, oc)
	_ = conv.EnsureRoomAgent(ctx, agent)
	turn := conv.StartTurn(ctx, agent, nil)
	turn.SetID(state.turnID)
	turn.SetSender(sender)
	turn.SetFinalMetadataProvider(sdk.FinalMetadataProviderFunc(func(_ *sdk.Turn, finishReason string) any {
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
	update := buildOpenClawUIMessageMetadata(sdk.UIMessageMetadataParams{
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
	}, state.sessionID, state.sessionKey, state.stream.ErrorText())
	if len(uiMessage) == 0 {
		return sdk.BuildUIMessage(sdk.UIMessageParams{
			TurnID:   state.turnID,
			Role:     stringutil.TrimDefault(state.role, "assistant"),
			Metadata: update,
		})
	}
	metadata, _ := uiMessage["metadata"].(map[string]any)
	uiMessage["metadata"] = sdk.MergeUIMessageMetadata(metadata, update)
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
	canonical := sdk.BuildCanonicalAssistantMetadata(sdk.CanonicalAssistantMetadataParams{
		UIMessage:        uiMessage,
		ToolType:         "openclaw",
		FinishReason:     state.stream.FinishReason(),
		TurnID:           state.turnID,
		AgentID:          state.agentID,
		Role:             stringutil.TrimDefault(state.role, "assistant"),
		Body:             body,
		StartedAtMs:      state.stream.StartedAtMs(),
		CompletedAtMs:    state.stream.CompletedAtMs(),
		PromptTokens:     state.promptTokens,
		CompletionTokens: state.completionTokens,
		ReasoningTokens:  state.reasoningTokens,
		FirstTokenAtMs:   state.stream.FirstTokenAtMs(),
		CompletionID:     state.runID,
	})
	return buildOpenClawMessageMetadata(openClawMessageMetadataParams{
		Base:           canonical.Bundle.Base,
		SessionID:      state.sessionID,
		SessionKey:     state.sessionKey,
		RunID:          canonical.Bundle.Assistant.CompletionID,
		ErrorText:      state.stream.ErrorText(),
		TotalTokens:    state.totalTokens,
		FirstTokenAtMs: canonical.Bundle.Assistant.FirstTokenAtMs,
	})
}
