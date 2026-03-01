package streamui

import (
	"context"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/matrixevents"
)

// UIState tracks AI SDK UIMessage stream state shared across bridges.
type UIState struct {
	TurnID                  string
	UIStarted               bool
	UIFinished              bool
	UITextID                string
	UIReasoningID           string
	UIStepOpen              bool
	UIStepCount             int
	UIToolStarted           map[string]bool
	UISourceURLSeen         map[string]bool
	UISourceDocumentSeen    map[string]bool
	UIFileSeen              map[string]bool
	UIToolOutputFinalized   map[string]bool
	UIToolCallIDByApproval  map[string]string
	UIToolApprovalRequested map[string]bool
	UIToolNameByToolCallID  map[string]string
	UIToolTypeByToolCallID  map[string]matrixevents.ToolType
}

// InitMaps initialises all nil maps so callers don't need nil checks.
func (s *UIState) InitMaps() {
	if s.UIToolStarted == nil {
		s.UIToolStarted = make(map[string]bool)
	}
	if s.UISourceURLSeen == nil {
		s.UISourceURLSeen = make(map[string]bool)
	}
	if s.UISourceDocumentSeen == nil {
		s.UISourceDocumentSeen = make(map[string]bool)
	}
	if s.UIFileSeen == nil {
		s.UIFileSeen = make(map[string]bool)
	}
	if s.UIToolOutputFinalized == nil {
		s.UIToolOutputFinalized = make(map[string]bool)
	}
	if s.UIToolCallIDByApproval == nil {
		s.UIToolCallIDByApproval = make(map[string]string)
	}
	if s.UIToolApprovalRequested == nil {
		s.UIToolApprovalRequested = make(map[string]bool)
	}
	if s.UIToolNameByToolCallID == nil {
		s.UIToolNameByToolCallID = make(map[string]string)
	}
	if s.UIToolTypeByToolCallID == nil {
		s.UIToolTypeByToolCallID = make(map[string]matrixevents.ToolType)
	}
}

// Emitter provides shared UI stream event emission.
// Bridges supply the Emit callback which delegates to bridge-specific transport.
type Emitter struct {
	State *UIState
	Emit  func(ctx context.Context, portal *bridgev2.Portal, part map[string]any)
}

// EmitUIStart sends the "start" event.
func (e *Emitter) EmitUIStart(ctx context.Context, portal *bridgev2.Portal, messageMetadata map[string]any) {
	if e.State.UIStarted {
		return
	}
	e.State.UIStarted = true
	e.Emit(ctx, portal, map[string]any{
		"type":            "start",
		"messageId":       e.State.TurnID,
		"messageMetadata": messageMetadata,
	})
}

// EmitUIMessageMetadata sends a "message-metadata" event.
func (e *Emitter) EmitUIMessageMetadata(ctx context.Context, portal *bridgev2.Portal, metadata map[string]any) {
	if len(metadata) == 0 {
		return
	}
	e.Emit(ctx, portal, map[string]any{
		"type":            "message-metadata",
		"messageMetadata": metadata,
	})
}

// EmitUIAbort sends an "abort" event.
func (e *Emitter) EmitUIAbort(ctx context.Context, portal *bridgev2.Portal, reason string) {
	part := map[string]any{"type": "abort"}
	if strings.TrimSpace(reason) != "" {
		part["reason"] = reason
	}
	e.Emit(ctx, portal, part)
}

// EmitUIStepStart sends a "start-step" event (idempotent while a step is open).
func (e *Emitter) EmitUIStepStart(ctx context.Context, portal *bridgev2.Portal) {
	if e.State.UIStepOpen {
		return
	}
	e.State.UIStepOpen = true
	e.State.UIStepCount++
	e.Emit(ctx, portal, map[string]any{"type": "start-step"})
}

// EmitUIStepFinish sends a "finish-step" event (no-op if no step is open).
func (e *Emitter) EmitUIStepFinish(ctx context.Context, portal *bridgev2.Portal) {
	if !e.State.UIStepOpen {
		return
	}
	e.State.UIStepOpen = false
	e.Emit(ctx, portal, map[string]any{"type": "finish-step"})
}

// EnsureUIText sends "text-start" the first time it's called for a turn.
func (e *Emitter) EnsureUIText(ctx context.Context, portal *bridgev2.Portal) {
	if e.State.UITextID != "" {
		return
	}
	e.State.UITextID = fmt.Sprintf("text-%s", e.State.TurnID)
	e.Emit(ctx, portal, map[string]any{
		"type": "text-start",
		"id":   e.State.UITextID,
	})
}

// EnsureUIReasoning sends "reasoning-start" the first time it's called for a turn.
func (e *Emitter) EnsureUIReasoning(ctx context.Context, portal *bridgev2.Portal) {
	if e.State.UIReasoningID != "" {
		return
	}
	e.State.UIReasoningID = fmt.Sprintf("reasoning-%s", e.State.TurnID)
	e.Emit(ctx, portal, map[string]any{
		"type": "reasoning-start",
		"id":   e.State.UIReasoningID,
	})
}

// EmitUITextDelta sends a "text-delta" event, ensuring text has started.
func (e *Emitter) EmitUITextDelta(ctx context.Context, portal *bridgev2.Portal, delta string) {
	e.EnsureUIText(ctx, portal)
	e.Emit(ctx, portal, map[string]any{
		"type":  "text-delta",
		"id":    e.State.UITextID,
		"delta": delta,
	})
}

// EmitUIReasoningDelta sends a "reasoning-delta" event, ensuring reasoning has started.
func (e *Emitter) EmitUIReasoningDelta(ctx context.Context, portal *bridgev2.Portal, delta string) {
	e.EnsureUIReasoning(ctx, portal)
	e.Emit(ctx, portal, map[string]any{
		"type":  "reasoning-delta",
		"id":    e.State.UIReasoningID,
		"delta": delta,
	})
}

// EmitUIError sends an "error" event.
func (e *Emitter) EmitUIError(ctx context.Context, portal *bridgev2.Portal, errText string) {
	if errText == "" {
		errText = "Unknown error"
	}
	e.Emit(ctx, portal, map[string]any{
		"type":      "error",
		"errorText": errText,
	})
}

// EmitUIFinish closes any open text/reasoning/step, finalises pending tools,
// and sends the "finish" event.
func (e *Emitter) EmitUIFinish(ctx context.Context, portal *bridgev2.Portal, finishReason string, messageMetadata map[string]any) {
	if e.State.UIFinished {
		return
	}
	e.State.UIFinished = true

	if e.State.UITextID != "" {
		e.Emit(ctx, portal, map[string]any{"type": "text-end", "id": e.State.UITextID})
		e.State.UITextID = ""
	}
	if e.State.UIReasoningID != "" {
		e.Emit(ctx, portal, map[string]any{"type": "reasoning-end", "id": e.State.UIReasoningID})
		e.State.UIReasoningID = ""
	}
	e.EmitUIStepFinish(ctx, portal)

	// Finalize any un-finished tool calls.
	for toolCallID := range e.State.UIToolStarted {
		if !e.State.UIToolOutputFinalized[toolCallID] {
			e.EmitUIToolOutputError(ctx, portal, toolCallID, "cancelled", false)
		}
	}

	e.Emit(ctx, portal, map[string]any{
		"type":            "finish",
		"finishReason":    finishReason,
		"messageMetadata": messageMetadata,
	})
}
