package sdk

import (
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/shared/streamui"
)

// TurnStream is the transport/escape-hatch surface for a turn.
type TurnStream struct {
	turnAccessor
}

type turnAccessor struct {
	turn *Turn
}

func (a *turnAccessor) valid() bool { return a != nil && a.turn != nil }

// Stream returns the turn's transport/escape-hatch surface.
func (t *Turn) Stream() *TurnStream {
	if t == nil {
		return nil
	}
	return &TurnStream{turnAccessor{turn: t}}
}

// Writer returns the turn's canonical semantic writer surface.
func (t *Turn) Writer() *Writer {
	if t == nil {
		return nil
	}
	return &Writer{
		State:   t.state,
		Emitter: t.emitter,
		Portal:  turnPortal(t),
		ensureStarted: func() {
			t.ensureStarted()
		},
		onText: func(text string) {
			t.mu.Lock()
			t.visibleText.WriteString(text)
			t.mu.Unlock()
		},
		onMetadata: func(metadata map[string]any) {
			t.mu.Lock()
			defer t.mu.Unlock()
			for k, v := range metadata {
				t.metadata[k] = v
			}
		},
	}
}

// VisibleText returns the raw text body accumulated through the semantic writer.
func (t *Turn) VisibleText() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	text := t.visibleText.String()
	t.mu.Unlock()
	if text != "" {
		return text
	}
	uiMessage := streamui.SnapshotUIMessage(t.UIState())
	if len(uiMessage) == 0 {
		return ""
	}
	td, ok := TurnDataFromUIMessage(uiMessage)
	if !ok {
		return ""
	}
	var visible strings.Builder
	for _, part := range td.Parts {
		if part.Type == "text" {
			visible.WriteString(part.Text)
		}
	}
	return visible.String()
}

func turnPortal(t *Turn) *bridgev2.Portal {
	if t == nil || t.conv == nil {
		return nil
	}
	return t.conv.portal
}

// Emitter returns the underlying stream emitter for advanced stream control.
func (s *TurnStream) Emitter() *streamui.Emitter {
	if !s.valid() {
		return nil
	}
	return s.turn.emitter
}

// SetTransport configures a custom transport for streamed turn events.
func (s *TurnStream) SetTransport(hook func(turnID string, seq int, content map[string]any, txnID string) bool) {
	if !s.valid() {
		return
	}
	s.turn.streamHook = hook
}

// Approvals returns the turn's approval controller.
func (t *Turn) Approvals() *ApprovalController {
	if t == nil {
		return nil
	}
	return &ApprovalController{turn: t}
}
