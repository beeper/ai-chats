package streamui

import (
	"context"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-chats/pkg/matrixevents"
)

// UIState tracks AI AI helper UIMessage stream state shared across bridges.
type UIState struct {
	TurnID                   string
	UIStarted                bool
	UIFinished               bool
	UITextID                 string
	UIReasoningID            string
	UIStepOpen               bool
	UIStepCount              int
	UIMessage                map[string]any
	UIToolStarted            map[string]bool
	UISourceURLSeen          map[string]bool
	UISourceDocumentSeen     map[string]bool
	UIFileSeen               map[string]bool
	UIToolOutputFinalized    map[string]bool
	UIToolCallIDByApproval   map[string]string
	UIToolApprovalRequested  map[string]bool
	UIToolNameByToolCallID   map[string]string
	UIToolTypeByToolCallID   map[string]matrixevents.ToolType
	UITextPartIndexByID      map[string]int
	UIReasoningPartIndexByID map[string]int
	UIToolPartIndexByID      map[string]int
	UIToolInputTextByID      map[string]string
}

// initMap initialises a nil map pointer so callers don't need nil checks.
func initMap[K comparable, V any](m *map[K]V) {
	if *m == nil {
		*m = make(map[K]V)
	}
}

// InitMaps initialises all nil maps so callers don't need nil checks.
func (s *UIState) InitMaps() {
	initMap(&s.UIToolStarted)
	initMap(&s.UISourceURLSeen)
	initMap(&s.UISourceDocumentSeen)
	initMap(&s.UIFileSeen)
	initMap(&s.UIToolOutputFinalized)
	initMap(&s.UIToolCallIDByApproval)
	initMap(&s.UIToolApprovalRequested)
	initMap(&s.UIToolNameByToolCallID)
	initMap(&s.UIToolTypeByToolCallID)
	initMap(&s.UITextPartIndexByID)
	initMap(&s.UIReasoningPartIndexByID)
	initMap(&s.UIToolPartIndexByID)
	initMap(&s.UIToolInputTextByID)
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

func (e *Emitter) ensureUIPartStarted(ctx context.Context, portal *bridgev2.Portal, idRef *string, partType string) {
	if idRef == nil || *idRef != "" {
		return
	}
	*idRef = fmt.Sprintf("%s-%s", partType, e.State.TurnID)
	e.Emit(ctx, portal, map[string]any{
		"type": partType + "-start",
		"id":   *idRef,
	})
}

// EmitUITextDelta sends a "text-delta" event, ensuring text has started.
func (e *Emitter) EmitUITextDelta(ctx context.Context, portal *bridgev2.Portal, delta string) {
	e.ensureUIPartStarted(ctx, portal, &e.State.UITextID, "text")
	e.Emit(ctx, portal, map[string]any{
		"type":  "text-delta",
		"id":    e.State.UITextID,
		"delta": delta,
	})
}

// EmitUIReasoningDelta sends a "reasoning-delta" event, ensuring reasoning has started.
func (e *Emitter) EmitUIReasoningDelta(ctx context.Context, portal *bridgev2.Portal, delta string) {
	e.ensureUIPartStarted(ctx, portal, &e.State.UIReasoningID, "reasoning")
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
