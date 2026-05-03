package codex

import (
	"context"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/pkg/matrixevents"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/sdk"
)

func (cc *CodexClient) runTurn(ctx context.Context, portal *bridgev2.Portal, portalState *codexPortalState, sourceEvent *event.Event, body string) {
	log := cc.loggerForContext(ctx)
	streamState := newStreamingState(sourceEvent.ID)

	model := cc.connector.Config.Codex.DefaultModel
	streamState.currentModel = model
	threadID := strings.TrimSpace(portalState.CodexThreadID)
	cwd := strings.TrimSpace(portalState.CodexCwd)
	conv := sdk.NewConversation(ctx, cc.UserLogin, portal, cc.senderForPortal(), cc.connector.sdkConfig, cc)
	source := sdk.UserMessageSource(sourceEvent.ID.String())
	turn := conv.StartTurn(ctx, codexSDKAgent(), source)
	approvals := turn.Approvals()
	if cc.streamEventHook != nil {
		turn.SetStreamHook(func(turnID string, seq int, content map[string]any, txnID string) bool {
			cc.streamEventHook(turnID, seq, content, txnID)
			return true
		})
	}
	approvals.SetHandler(func(callCtx context.Context, sdkTurn *sdk.Turn, req sdk.ApprovalRequest) sdk.ApprovalHandle {
		return cc.requestSDKApproval(callCtx, portal, streamState, sdkTurn, req)
	})
	turn.SetFinalMetadataProvider(sdk.FinalMetadataProviderFunc(func(sdkTurn *sdk.Turn, finishReason string) any {
		return cc.buildSDKFinalMetadata(sdkTurn, streamState, codexStateModel(streamState, model), finishReason)
	}))
	streamState.turn = turn
	streamState.agentID = string(codexGhostID)
	turn.Writer().MessageMetadata(ctx, cc.buildUIMessageMetadata(streamState, codexStateModel(streamState, model), false, ""))
	turn.Writer().StepStart(ctx)

	approvalPolicy := "untrusted"
	if lvl, _ := stringutil.NormalizeElevatedLevel(portalState.ElevatedLevel); lvl == "full" {
		approvalPolicy = "never"
	}

	// Start turn.
	var turnStart struct {
		Turn struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"turn"`
	}
	turnStartCtx, cancelTurnStart := context.WithTimeout(ctx, 60*time.Second)
	defer cancelTurnStart()
	err := cc.rpc.Call(turnStartCtx, "turn/start", map[string]any{
		"threadId": threadID,
		"input": []map[string]any{
			{"type": "text", "text": body},
		},
		"cwd":            cwd,
		"approvalPolicy": approvalPolicy,
		"sandboxPolicy":  cc.buildSandboxPolicy(cwd),
	}, &turnStart)
	if err != nil {
		turn.EndWithError(err.Error())
		return
	}
	turnID := strings.TrimSpace(turnStart.Turn.ID)
	if turnID == "" {
		turn.EndWithError("Codex turn/start response missing turn id")
		return
	}
	turnCh := cc.subscribeTurn(threadID, turnID)
	defer cc.unsubscribeTurn(threadID, turnID)

	cc.activeMu.Lock()
	cc.activeTurns[codexTurnKey(threadID, turnID)] = &codexActiveTurn{
		portal:      portal,
		portalState: portalState,
		streamState: streamState,
		threadID:    threadID,
		turnID:      turnID,
		model:       model,
	}
	cc.activeMu.Unlock()
	defer func() {
		cc.activeMu.Lock()
		delete(cc.activeTurns, codexTurnKey(threadID, turnID))
		cc.activeMu.Unlock()
	}()

	finishStatus := "completed"
	var completedErr string
	maxWait := time.NewTimer(10 * time.Minute)
	defer maxWait.Stop()
	for {
		select {
		case evt := <-turnCh:
			cc.handleNotif(ctx, portal, portalState, streamState, model, threadID, turnID, evt)
			if st, errText, ok := codexTurnCompletedStatus(evt, threadID, turnID); ok {
				finishStatus = st
				completedErr = errText
				goto done
			}
			maxWait.Reset(10 * time.Minute)
		case <-maxWait.C:
			finishStatus = "timeout"
			goto done
		case <-ctx.Done():
			finishStatus = "interrupted"
			goto done
		}
	}

done:
	log.Debug().Str("status", finishStatus).Str("thread", threadID).Str("turn", turnID).Msg("Codex turn finished")
	streamState.completedAtMs = time.Now().UnixMilli()
	// If we observed turn-level diff updates, finalize them as a dedicated tool output.
	if diff := strings.TrimSpace(streamState.codexLatestDiff); diff != "" {
		diffToolID := fmt.Sprintf("diff-%s", turnID)
		emitDiffToolOutput(ctx, streamState, diffToolID, turnID, diff, false)
		streamState.toolCalls = append(streamState.toolCalls, ToolCallMetadata{
			CallID:        diffToolID,
			ToolName:      "diff",
			ToolType:      string(matrixevents.ToolTypeProvider),
			Input:         map[string]any{"turnId": turnID},
			Output:        map[string]any{"diff": diff},
			Status:        string(matrixevents.ToolStatusCompleted),
			ResultStatus:  string(matrixevents.ResultStatusSuccess),
			StartedAtMs:   streamState.startedAtMs,
			CompletedAtMs: streamState.completedAtMs,
		})
	}
	if completedErr != "" {
		streamState.turn.Writer().MessageMetadata(ctx, cc.buildUIMessageMetadata(streamState, codexStateModel(streamState, model), true, finishStatus))
		streamState.turn.EndWithError(completedErr)
		return
	}
	streamState.turn.Writer().MessageMetadata(ctx, cc.buildUIMessageMetadata(streamState, codexStateModel(streamState, model), true, finishStatus))
	streamState.turn.End(finishStatus)
}

func (cc *CodexClient) appendCodexToolOutput(state *streamingState, toolCallID, delta string) string {
	if state == nil || toolCallID == "" {
		return delta
	}
	if state.codexToolOutputBuffers == nil {
		state.codexToolOutputBuffers = make(map[string]*strings.Builder)
	}
	b := state.codexToolOutputBuffers[toolCallID]
	if b == nil {
		b = &strings.Builder{}
		state.codexToolOutputBuffers[toolCallID] = b
	}
	b.WriteString(delta)
	return b.String()
}

// emitDiffToolOutput emits a diff tool output via the SDK writer.
func emitDiffToolOutput(ctx context.Context, state *streamingState, diffToolID, turnID, diff string, streaming bool) {
	if state == nil || state.turn == nil {
		return
	}
	state.turn.Writer().Tools().EnsureInputStart(ctx, diffToolID, map[string]any{"turnId": turnID}, sdk.ToolInputOptions{
		ToolName:         "diff",
		ProviderExecuted: true,
	})
	state.turn.Writer().Tools().Output(ctx, diffToolID, diff, sdk.ToolOutputOptions{
		ProviderExecuted: true,
		Streaming:        streaming,
	})
}

func codexStateModel(state *streamingState, fallback string) string {
	if state != nil {
		if model := strings.TrimSpace(state.currentModel); model != "" {
			return model
		}
	}
	return strings.TrimSpace(fallback)
}

// codexNotifFields holds the common fields present in most Codex notifications.
