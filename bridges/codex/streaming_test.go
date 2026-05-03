package codex

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/shared/jsonutil"
	"github.com/beeper/agentremote/pkg/shared/streamui"
)

func TestCodex_StreamChunks_BasicOrderingAndSeq(t *testing.T) {
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	state := newHookableStreamingState("turn_local_1")
	attachTestTurn(state, portal)
	state.turn.Writer().MessageMetadata(state.turn.Context(), map[string]any{"model": "gpt-5.1-codex"})
	state.turn.Writer().StepStart(state.turn.Context())
	state.turn.Writer().TextDelta(state.turn.Context(), "hi")
	state.turn.End("completed")

	uiState := state.turn.UIState()
	if uiState == nil || !uiState.UIStarted || !uiState.UIFinished {
		t.Fatalf("expected turn UI state to be started and finished, got %#v", uiState)
	}
	uiMessage := streamui.SnapshotUIMessage(uiState)
	var gotParts []map[string]any
	switch typed := uiMessage["parts"].(type) {
	case []map[string]any:
		gotParts = typed
	case []any:
		gotParts = make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			part := jsonutil.ToMap(item)
			if len(part) == 0 {
				continue
			}
			gotParts = append(gotParts, part)
		}
	}
	if len(gotParts) == 0 {
		t.Fatal("expected UI message parts")
	}
	seenText := false
	for _, part := range gotParts {
		if part["type"] == "text" {
			seenText = true
			break
		}
	}
	if !seenText {
		t.Fatalf("expected canonical text part, got %#v", gotParts)
	}
}
