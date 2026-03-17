package openclaw

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/shared/streamui"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

func newOpenClawTestTurn(turnID string) *bridgesdk.Turn {
	conv := bridgesdk.NewConversation(context.Background(), nil, nil, bridgev2.EventSender{}, &bridgesdk.Config{}, nil)
	turn := conv.StartTurn(context.Background(), nil, nil)
	turn.SetID(turnID)
	return turn
}

func TestComputeVisibleDeltaTracksPrefixOnly(t *testing.T) {
	oc := &OpenClawClient{
		streamStates: map[string]*openClawStreamState{
			"turn-1": {turnID: "turn-1"},
		},
	}

	if got := oc.computeVisibleDelta("turn-1", "hello"); got != "hello" {
		t.Fatalf("expected first delta to be full text, got %q", got)
	}
	if got := oc.computeVisibleDelta("turn-1", "hello world"); got != " world" {
		t.Fatalf("expected suffix delta, got %q", got)
	}
	if got := oc.computeVisibleDelta("turn-1", "hello world"); got != "" {
		t.Fatalf("expected no delta for unchanged text, got %q", got)
	}
}

func TestIsStreamActiveReflectsStatePresence(t *testing.T) {
	oc := &OpenClawClient{
		streamStates: map[string]*openClawStreamState{
			"turn-2": {turnID: "turn-2"},
		},
	}
	if !oc.isStreamActive("turn-2") {
		t.Fatal("expected active stream state")
	}
	if oc.isStreamActive("missing") {
		t.Fatal("did not expect missing stream state to be active")
	}
}

func TestBuildStreamDBMetadataIncludesToolCalls(t *testing.T) {
	oc := &OpenClawClient{}
	state := &openClawStreamState{
		turnID:     "turn-3",
		agentID:    "main",
		sessionID:  "sess-1",
		sessionKey: "agent:main:matrix-dm",
		role:       "assistant",
		turn:       newOpenClawTestTurn("turn-3"),
	}
	state.stream.ApplyPart(map[string]any{"type": "text-delta", "delta": "running"}, time.Time{})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{
		"type": "reasoning-start",
		"id":   "reasoning-1",
	})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{
		"type":  "reasoning-delta",
		"id":    "reasoning-1",
		"delta": "thinking",
	})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{
		"type": "reasoning-end",
		"id":   "reasoning-1",
	})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{
		"type":       "tool-input-available",
		"toolCallId": "call-1",
		"toolName":   "bash",
		"input":      map[string]any{"cmd": "pwd"},
	})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{
		"type":       "tool-output-available",
		"toolCallId": "call-1",
		"output":     map[string]any{"stdout": "/tmp"},
	})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{
		"type":      "file",
		"url":       "mxc://example.org/out",
		"mediaType": "image/png",
	})

	meta := oc.buildStreamDBMetadata(state)
	if meta == nil {
		t.Fatal("expected metadata")
	}
	if meta.ThinkingContent != "thinking" {
		t.Fatalf("unexpected thinking content: %q", meta.ThinkingContent)
	}
	if len(meta.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %#v", meta.ToolCalls)
	}
	call := meta.ToolCalls[0]
	if call.CallID != "call-1" || call.ToolName != "bash" || call.ToolType != "openclaw" {
		t.Fatalf("unexpected tool call metadata: %#v", call)
	}
	if call.Status != "output-available" || call.ResultStatus != "completed" {
		t.Fatalf("unexpected tool call status: %#v", call)
	}
	if call.Input["cmd"] != "pwd" {
		t.Fatalf("unexpected tool input: %#v", call.Input)
	}
	if call.Output["stdout"] != "/tmp" {
		t.Fatalf("unexpected tool output: %#v", call.Output)
	}
	if len(meta.GeneratedFiles) != 1 {
		t.Fatalf("expected 1 generated file, got %#v", meta.GeneratedFiles)
	}
	if meta.GeneratedFiles[0].URL != "mxc://example.org/out" || meta.GeneratedFiles[0].MimeType != "image/png" {
		t.Fatalf("unexpected generated files: %#v", meta.GeneratedFiles)
	}
}

func TestApplyStreamPartStateLockedUpdatesLifecycleFields(t *testing.T) {
	oc := &OpenClawClient{}
	state := &openClawStreamState{}

	oc.applyStreamPartStateLocked(state, map[string]any{
		"type":      "text-delta",
		"delta":     "hello",
		"timestamp": float64(time.Now().UnixMilli()),
	})
	if got := state.stream.VisibleText(); got != "hello" {
		t.Fatalf("expected visible text to accumulate delta, got %q", got)
	}
	if got := state.stream.AccumulatedText(); got != "hello" {
		t.Fatalf("expected accumulated text to include delta, got %q", got)
	}
	if state.stream.StartedAtMs() == 0 || state.stream.FirstTokenAtMs() == 0 {
		t.Fatalf("expected lifecycle timestamps to be tracked, got started=%d first_token=%d", state.stream.StartedAtMs(), state.stream.FirstTokenAtMs())
	}

	oc.applyStreamPartStateLocked(state, map[string]any{
		"type":      "error",
		"errorText": "boom",
	})
	if state.stream.ErrorText() != "boom" {
		t.Fatalf("expected error text to be captured, got %q", state.stream.ErrorText())
	}
}

func TestCompleteStreamTurnRemovesTrackedState(t *testing.T) {
	turn := newOpenClawTestTurn("turn-1")
	state := &openClawStreamState{
		turnID: "turn-1",
		turn:   turn,
	}
	state.stream.SetFinishReason("stop")
	oc := &OpenClawClient{
		streamStates: map[string]*openClawStreamState{
			"turn-1": state,
		},
	}

	oc.completeStreamTurn("turn-1", state, turn)
	if _, ok := oc.streamStates["turn-1"]; ok {
		t.Fatal("expected turn state to be removed after completion")
	}
}

func TestDrainStreamTurnsResetsMapAndReturnsActiveTurns(t *testing.T) {
	active := new(bridgesdk.Turn)
	oc := &OpenClawClient{
		streamStates: map[string]*openClawStreamState{
			"turn-active": {turnID: "turn-active", turn: active},
			"turn-empty":  {turnID: "turn-empty"},
		},
	}

	turns := oc.drainStreamTurns()
	if len(turns) != 1 {
		t.Fatalf("expected exactly 1 active turn, got %d", len(turns))
	}
	if turns[0] != active {
		t.Fatal("expected returned turn pointer to match active state")
	}
	if len(oc.streamStates) != 0 {
		t.Fatalf("expected stream state map to be reset, got %d entries", len(oc.streamStates))
	}
}
