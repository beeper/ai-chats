package sdk

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/pkg/shared/streamui"
	"github.com/beeper/agentremote/turns"
)

func (t *Turn) finalMetadata(finishReason string) BaseMessageMetadata {
	uiMessage := streamui.SnapshotUIMessage(t.state)
	turnData := BuildTurnDataFromUIMessage(uiMessage, TurnDataBuildOptions{
		ID:   t.turnID,
		Role: "assistant",
		Text: strings.TrimSpace(t.VisibleText()),
	})
	var agentID string
	if t.agent != nil {
		agentID = t.agent.ID
	}
	runtimeMeta := BuildAssistantMetadataBundle(AssistantMetadataBundleParams{
		TurnData:      turnData,
		FinishReason:  finishReason,
		TurnID:        t.turnID,
		AgentID:       agentID,
		StartedAtMs:   t.startedAtMs,
		CompletedAtMs: time.Now().UnixMilli(),
	}).Base
	merged := supportedBaseMetadataFromMap(t.metadata)
	merged.CopyFromBase(&runtimeMeta)
	return merged
}
func (t *Turn) buildFinalEdit() (networkid.MessageID, *bridgev2.ConvertedEdit) {
	if t == nil {
		return "", nil
	}
	payload := t.finalEditPayload
	if payload == nil || payload.Content == nil {
		return "", nil
	}
	target := t.networkMessageID
	if target == "" {
		target = MatrixMessageID(t.initialEventID)
	}
	if target == "" {
		return "", nil
	}
	fittedPayload, fitDetails, err := FitFinalEditPayload(payload, t.initialEventID)
	if err != nil {
		fallbackPayload := cloneFinalEditPayload(payload)
		if fallbackPayload != nil {
			fallbackPayload.Extra = nil
			fallbackPayload.TopLevelExtra = nil
		}
		fallbackFittedPayload, fallbackFitDetails, fallbackErr := FitFinalEditPayload(fallbackPayload, t.initialEventID)
		if fallbackErr == nil {
			fittedPayload = fallbackFittedPayload
			fitDetails = fallbackFitDetails
			err = nil
		} else if t.conv != nil && t.conv.login != nil {
			t.conv.login.Log.Warn().
				Str("component", "sdk_turn").
				Int("original_bytes", fitDetails.OriginalSize).
				Int("original_final_bytes", fitDetails.FinalSize).
				Err(err).
				Int("fallback_bytes", fallbackFitDetails.OriginalSize).
				Int("fallback_final_bytes", fallbackFitDetails.FinalSize).
				Str("fallback_error", fallbackErr.Error()).
				Msg("Skipped final edit because payload could not fit Matrix content limits")
			return "", nil
		} else {
			return "", nil
		}
	}
	if fittedPayload == nil || fittedPayload.Content == nil {
		return "", nil
	}
	if fitDetails.Changed() && t.conv != nil && t.conv.login != nil {
		t.conv.login.Log.Warn().
			Str("component", "sdk_turn").
			Int("original_bytes", fitDetails.OriginalSize).
			Int("final_bytes", fitDetails.FinalSize).
			Str("reductions", fitDetails.Summary()).
			Msg("Reduced final edit payload to fit Matrix content limits")
	}
	content := *fittedPayload.Content
	if content.Mentions == nil {
		content.Mentions = &event.Mentions{}
	}
	content.RelatesTo = nil
	extra := maps.Clone(fittedPayload.Extra)
	topLevelExtra := maps.Clone(fittedPayload.TopLevelExtra)
	if extra == nil {
		extra = map[string]any{}
	}
	if topLevelExtra == nil {
		topLevelExtra = map[string]any{}
	}
	if t.session != nil {
		// Explicitly clear the live-stream descriptor on terminal edits so the
		// edited event no longer looks like an active placeholder.
		content.BeeperStream = nil
		extra["com.beeper.stream"] = nil
		topLevelExtra["com.beeper.stream"] = nil
	}
	if t.initialEventID != "" {
		topLevelExtra["m.relates_to"] = (&event.RelatesTo{}).SetReplace(t.initialEventID)
	}
	return target, &bridgev2.ConvertedEdit{
		ModifiedParts: []*bridgev2.ConvertedEditPart{{
			Type:          event.EventMessage,
			Content:       &content,
			Extra:         extra,
			TopLevelExtra: topLevelExtra,
		}},
	}
}
func (t *Turn) sendFinalEdit(ctx context.Context, finishReason string) {
	if t == nil || t.conv == nil || t.conv.login == nil || t.conv.portal == nil {
		return
	}
	target, edit := t.buildFinalEdit()
	if target == "" || edit == nil {
		return
	}
	sender := t.resolveSender(ctx)
	metadata := any(t.finalMetadata(finishReason))
	if t.finalMetadataProvider != nil {
		if custom := t.finalMetadataProvider.FinalMetadata(t, finishReason); custom != nil {
			metadata = custom
		}
	}
	if err := t.ensureSenderJoined(ctx, sender, bridgev2.RemoteEventMessage); err != nil && t.conv.login != nil {
		t.conv.login.Log.Warn().Err(err).Str("component", "sdk_turn").Msg("Failed to join sender before final turn edit")
	}
	if err := SendEditViaPortal(
		t.conv.login,
		t.conv.portal,
		sender,
		target,
		time.Now(),
		0,
		"sdk_edit_target",
		edit,
		metadata,
	); err != nil && t.conv.login != nil {
		t.conv.login.Log.Warn().Err(err).Str("component", "sdk_turn").Msg("Failed to send final turn edit")
	}
}
func (t *Turn) dispatchFinalEdit(ctx context.Context, finishReason string) {
	if t == nil {
		return
	}
	if t.sendFinalEditFunc != nil {
		t.sendFinalEditFunc(ctx)
		return
	}
	t.sendFinalEdit(ctx, finishReason)
}
func supportedBaseMetadataFromMap(metadata map[string]any) BaseMessageMetadata {
	if len(metadata) == 0 {
		return BaseMessageMetadata{}
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return BaseMessageMetadata{}
	}
	var decoded BaseMessageMetadata
	if err = json.Unmarshal(data, &decoded); err != nil {
		return BaseMessageMetadata{}
	}
	return decoded
}

// End finishes the turn with a reason.
func (t *Turn) End(finishReason string) {
	t.mu.Lock()
	if t.ended {
		t.mu.Unlock()
		return
	}
	if !t.started {
		t.ended = true
		t.mu.Unlock()
		t.cancel()
		return
	}
	t.ended = true
	t.mu.Unlock()
	t.stopIdleTimeout()
	defer t.cancel()
	t.Writer().Finish(t.turnCtx, finishReason, t.metadata)
	t.finalizeTurn(turns.EndReasonFinish, finishReason, "")
}

// EndWithError finishes the turn with an error.
func (t *Turn) EndWithError(errText string) {
	t.mu.Lock()
	if t.ended {
		t.mu.Unlock()
		return
	}
	t.ended = true
	started := t.started
	t.mu.Unlock()
	t.stopIdleTimeout()
	defer t.cancel()
	if !started {
		// No content was ever written — skip placeholder message creation.
		// Still send a fail status if we have a source event.
		t.SendStatus(event.MessageStatusFail, errText)
		return
	}
	t.Writer().Error(t.turnCtx, errText)
	t.SendStatus(event.MessageStatusFail, errText)
	t.Writer().Finish(t.turnCtx, "error", t.metadata)
	t.finalizeTurn(turns.EndReasonError, "error", errText)
}

// Abort aborts the turn.
func (t *Turn) Abort(reason string) {
	t.mu.Lock()
	if t.ended {
		t.mu.Unlock()
		return
	}
	t.ended = true
	started := t.started
	t.mu.Unlock()
	t.stopIdleTimeout()
	defer t.cancel()
	if !started {
		// No content was ever written — skip placeholder message creation.
		t.SendStatus(event.MessageStatusRetriable, reason)
		return
	}
	t.Writer().Abort(t.turnCtx, reason)
	t.finalizeTurn(turns.EndReasonDisconnect, "abort", reason)
}
func (t *Turn) finalizeTurn(endReason turns.EndReason, finishReason, fallbackBody string) {
	finalCtx := t.finalizationContext()
	t.flushPendingStream(finalCtx)
	t.ensureDefaultFinalEditPayload(finishReason, fallbackBody)
	if t.session != nil {
		t.session.End(finalCtx, endReason)
	}
	t.dispatchFinalEdit(finalCtx, finishReason)
}
func (t *Turn) defaultFinalEditPayload(finishReason, fallbackBody string) *FinalEditPayload {
	if t == nil {
		return nil
	}
	uiMessage := streamui.SnapshotUIMessage(t.state)
	t.mu.Lock()
	body := strings.TrimSpace(t.visibleText.String())
	t.mu.Unlock()
	if body == "" {
		if td, ok := TurnDataFromUIMessage(uiMessage); ok {
			body = TurnText(td)
		}
	}
	fallbackBody = strings.TrimSpace(fallbackBody)
	uiMessage = BuildCompactFinalUIMessage(uiMessage)
	if body == "" && fallbackBody == "" && !hasMeaningfulFinalUIMessage(uiMessage) {
		return nil
	}
	if body == "" {
		body = fallbackBody
	}
	if body == "" {
		switch strings.TrimSpace(finishReason) {
		case "error":
			body = "Response failed"
		case "abort", "disconnect":
			body = "Response interrupted"
		default:
			body = "Completed response"
		}
	}
	return BuildFinalEditPayload(event.MessageEventContent{
		MsgType:  event.MsgText,
		Body:     body,
		Mentions: &event.Mentions{},
	}, uiMessage, nil, finishReason)
}
func (t *Turn) ensureDefaultFinalEditPayload(finishReason, fallbackBody string) {
	if t == nil || t.suppressFinalEdit {
		return
	}
	if t.finalEditPayload != nil && t.finalEditPayload.Content != nil {
		return
	}
	payload := t.defaultFinalEditPayload(finishReason, fallbackBody)
	if payload == nil || payload.Content == nil {
		return
	}
	t.finalEditPayload = payload
}
