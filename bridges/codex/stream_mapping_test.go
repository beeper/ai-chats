package codex

import (
	"context"
	"encoding/json"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	bridgesdk "github.com/beeper/agentremote/sdk"
)

func newHookableStreamingState(turnID string) *streamingState {
	return &streamingState{
		turnID:           turnID,
		initialEventID:   id.EventID("$event"),
		networkMessageID: networkid.MessageID("codex:test"),
	}
}

func attachTestTurn(state *streamingState, portal *bridgev2.Portal) {
	if state == nil {
		return
	}
	conv := bridgesdk.NewConversation(context.Background(), nil, portal, bridgev2.EventSender{}, &bridgesdk.Config{}, nil)
	turn := conv.StartTurn(context.Background(), nil, nil)
	turn.SetID(state.turnID)
	state.turn = turn
}

func TestCodex_Mapping_AgentMessageDelta_EmitsTextStartThenDelta(t *testing.T) {
	cc := &CodexClient{}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	state := newHookableStreamingState("turn_1")
	attachTestTurn(state, portal)
	threadID := "thr_1"
	turnID := "turn_1_server"

	params := map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
		"itemId":   "it_msg",
		"delta":    "hi",
	}
	raw, _ := json.Marshal(params)
	cc.handleNotif(context.Background(), portal, nil, state, "model", threadID, turnID, codexNotif{
		Method: "item/agentMessage/delta",
		Params: raw,
	})

	if got := state.accumulated.String(); got != "hi" {
		t.Fatalf("expected accumulated text %q, got %q", "hi", got)
	}
}

func TestCodex_Mapping_ReasoningSummaryDelta_EmitsReasoningStartThenDelta(t *testing.T) {
	cc := &CodexClient{}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	state := newHookableStreamingState("turn_1")
	attachTestTurn(state, portal)
	threadID := "thr_1"
	turnID := "turn_1_server"

	params := map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
		"delta":    "think",
	}
	raw, _ := json.Marshal(params)
	cc.handleNotif(context.Background(), portal, nil, state, "model", threadID, turnID, codexNotif{
		Method: "item/reasoning/summaryTextDelta",
		Params: raw,
	})

	if got := state.reasoning.String(); got != "think" {
		t.Fatalf("expected reasoning text %q, got %q", "think", got)
	}
}

func TestCodex_Mapping_ItemStartedCommandExecution_EmitsToolInputStartAndAvailable(t *testing.T) {
	cc := &CodexClient{}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	state := newHookableStreamingState("turn_1")
	attachTestTurn(state, portal)
	threadID := "thr_1"
	turnID := "turn_1_server"

	item := map[string]any{
		"type":    "commandExecution",
		"id":      "it_cmd",
		"command": []string{"ls", "-la"},
		"cwd":     "/tmp",
		"status":  "inProgress",
	}
	params := map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
		"item":     item,
	}
	raw, _ := json.Marshal(params)
	cc.handleNotif(context.Background(), portal, nil, state, "model", threadID, turnID, codexNotif{
		Method: "item/started",
		Params: raw,
	})

	if state.turn == nil {
		t.Fatal("expected SDK turn to exist")
	}
}

func TestCodex_Mapping_CommandOutputDelta_IsBuffered(t *testing.T) {
	cc := &CodexClient{}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	state := newHookableStreamingState("turn_1")
	attachTestTurn(state, portal)
	threadID := "thr_1"
	turnID := "turn_1_server"

	raw1, _ := json.Marshal(map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
		"itemId":   "it_cmd",
		"delta":    "hello",
	})
	raw2, _ := json.Marshal(map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
		"itemId":   "it_cmd",
		"delta":    " world",
	})
	cc.handleNotif(context.Background(), portal, nil, state, "model", threadID, turnID, codexNotif{
		Method: "item/commandExecution/outputDelta",
		Params: raw1,
	})
	cc.handleNotif(context.Background(), portal, nil, state, "model", threadID, turnID, codexNotif{
		Method: "item/commandExecution/outputDelta",
		Params: raw2,
	})

	if got := state.codexToolOutputBuffers["it_cmd"].String(); got != "hello world" {
		t.Fatalf("expected buffered output 'hello world', got %q", got)
	}
}

func TestCodex_Mapping_TurnDiffUpdated_EmitsToolOutput(t *testing.T) {
	cc := &CodexClient{}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	state := newHookableStreamingState("turn_1")
	attachTestTurn(state, portal)
	threadID := "thr_1"
	turnID := "turn_1_server"

	raw, _ := json.Marshal(map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
		"diff":     "diff --git a/x b/x",
	})
	cc.handleNotif(context.Background(), portal, nil, state, "model", threadID, turnID, codexNotif{
		Method: "turn/diff/updated",
		Params: raw,
	})

	// tool-input-start, tool-input-available, tool-output-available
	if state.codexLatestDiff != "diff --git a/x b/x" {
		t.Fatalf("expected diff to be stored, got %q", state.codexLatestDiff)
	}
}

func TestCodex_Mapping_ModelRerouted_UpdatesCurrentModel(t *testing.T) {
	cc := &CodexClient{
		activeTurns: make(map[string]*codexActiveTurn),
	}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	state := newHookableStreamingState("turn_1")
	state.currentModel = "gpt-5.1-codex"
	attachTestTurn(state, portal)
	threadID := "thr_1"
	turnID := "turn_1_server"
	cc.activeTurns[codexTurnKey(threadID, turnID)] = &codexActiveTurn{
		portal:   portal,
		state:    state,
		threadID: threadID,
		turnID:   turnID,
		model:    state.currentModel,
	}

	raw, _ := json.Marshal(map[string]any{
		"threadId":  threadID,
		"turnId":    turnID,
		"fromModel": "gpt-5.1-codex",
		"toModel":   "gpt-5-mini",
		"reason":    "safety",
	})
	cc.handleNotif(context.Background(), portal, nil, state, "gpt-5.1-codex", threadID, turnID, codexNotif{
		Method: "model/rerouted",
		Params: raw,
	})

	if state.currentModel != "gpt-5-mini" {
		t.Fatalf("expected current model to be updated, got %q", state.currentModel)
	}
	if active := cc.activeTurns[codexTurnKey(threadID, turnID)]; active == nil || active.model != "gpt-5-mini" {
		t.Fatalf("expected active turn model to be updated, got %#v", active)
	}
}

func TestCodex_Mapping_ContextCompaction_EmitsToolParts(t *testing.T) {
	cc := &CodexClient{}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	state := newHookableStreamingState("turn_1")
	attachTestTurn(state, portal)
	threadID := "thr_1"
	turnID := "turn_1_server"

	itemStarted := map[string]any{"type": "contextCompaction", "id": "it_cc"}
	rawStarted, _ := json.Marshal(map[string]any{"threadId": threadID, "turnId": turnID, "item": itemStarted})
	cc.handleNotif(context.Background(), portal, nil, state, "model", threadID, turnID, codexNotif{
		Method: "item/started",
		Params: rawStarted,
	})

	itemCompleted := map[string]any{"type": "contextCompaction", "id": "it_cc"}
	rawCompleted, _ := json.Marshal(map[string]any{"threadId": threadID, "turnId": turnID, "item": itemCompleted})
	cc.handleNotif(context.Background(), portal, nil, state, "model", threadID, turnID, codexNotif{
		Method: "item/completed",
		Params: rawCompleted,
	})

	// started => tool-input-start/tool-input-available, completed => tool-output-available
	if len(state.toolCalls) == 0 {
		t.Fatal("expected completed tool call metadata")
	}
	if state.toolCalls[len(state.toolCalls)-1].ToolName != "contextCompaction" {
		t.Fatalf("expected contextCompaction tool call, got %#v", state.toolCalls[len(state.toolCalls)-1])
	}
}

func TestCodex_Mapping_ReviewMode_EmitsReviewToolOutput(t *testing.T) {
	cc := &CodexClient{}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	state := newHookableStreamingState("turn_1")
	attachTestTurn(state, portal)
	threadID := "thr_1"
	turnID := "turn_1_server"

	rawStarted, _ := json.Marshal(map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
		"item":     map[string]any{"type": "enteredReviewMode", "id": "it_review", "review": "current changes"},
	})
	cc.handleNotif(context.Background(), portal, nil, state, "model", threadID, turnID, codexNotif{Method: "item/started", Params: rawStarted})

	rawCompleted, _ := json.Marshal(map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
		"item":     map[string]any{"type": "exitedReviewMode", "id": "it_review", "review": "Looks good"},
	})
	cc.handleNotif(context.Background(), portal, nil, state, "model", threadID, turnID, codexNotif{Method: "item/completed", Params: rawCompleted})

	if len(state.toolCalls) == 0 {
		t.Fatal("expected review tool call metadata")
	}
	if state.toolCalls[len(state.toolCalls)-1].ToolName != "review" {
		t.Fatalf("expected review tool call, got %#v", state.toolCalls[len(state.toolCalls)-1])
	}
}
