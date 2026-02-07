package connector

import (
	"context"
	"encoding/json"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"
)

func TestCodex_Mapping_AgentMessageDelta_EmitsTextStartThenDelta(t *testing.T) {
	cc := &CodexClient{}
	var got []string
	cc.streamEventHook = func(turnID string, seq int, content map[string]any, txnID string) {
		_ = turnID
		_ = seq
		_ = txnID
		part, _ := content["part"].(map[string]any)
		typ, _ := part["type"].(string)
		got = append(got, typ)
	}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	state := &streamingState{turnID: "turn_1"}
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

	if len(got) != 2 || got[0] != "text-start" || got[1] != "text-delta" {
		t.Fatalf("expected [text-start text-delta], got %v", got)
	}
}

func TestCodex_Mapping_ReasoningSummaryDelta_EmitsReasoningStartThenDelta(t *testing.T) {
	cc := &CodexClient{}
	var got []string
	cc.streamEventHook = func(turnID string, seq int, content map[string]any, txnID string) {
		_ = turnID
		_ = seq
		_ = txnID
		part, _ := content["part"].(map[string]any)
		typ, _ := part["type"].(string)
		got = append(got, typ)
	}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	state := &streamingState{turnID: "turn_1"}
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

	if len(got) != 2 || got[0] != "reasoning-start" || got[1] != "reasoning-delta" {
		t.Fatalf("expected [reasoning-start reasoning-delta], got %v", got)
	}
}

func TestCodex_Mapping_ItemStartedCommandExecution_EmitsToolInputStartAndAvailable(t *testing.T) {
	cc := &CodexClient{}
	var got []string
	cc.streamEventHook = func(turnID string, seq int, content map[string]any, txnID string) {
		_ = turnID
		_ = seq
		_ = txnID
		part, _ := content["part"].(map[string]any)
		typ, _ := part["type"].(string)
		got = append(got, typ)
	}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	state := &streamingState{turnID: "turn_1"}
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

	if len(got) != 2 || got[0] != "tool-input-start" || got[1] != "tool-input-available" {
		t.Fatalf("expected [tool-input-start tool-input-available], got %v", got)
	}
}

func TestCodex_Mapping_CommandOutputDelta_IsBuffered(t *testing.T) {
	cc := &CodexClient{}
	var gotOutputs []string
	cc.streamEventHook = func(turnID string, seq int, content map[string]any, txnID string) {
		_ = turnID
		_ = seq
		_ = txnID
		part, _ := content["part"].(map[string]any)
		if part["type"] != "tool-output-available" {
			return
		}
		if out, ok := part["output"].(string); ok {
			gotOutputs = append(gotOutputs, out)
		}
	}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	state := &streamingState{turnID: "turn_1"}
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

	if len(gotOutputs) < 2 {
		t.Fatalf("expected at least 2 tool outputs, got %v", gotOutputs)
	}
	if gotOutputs[len(gotOutputs)-1] != "hello world" {
		t.Fatalf("expected buffered output 'hello world', got %q", gotOutputs[len(gotOutputs)-1])
	}
}

func TestCodex_Mapping_TurnDiffUpdated_EmitsToolOutput(t *testing.T) {
	cc := &CodexClient{}
	var got []string
	cc.streamEventHook = func(turnID string, seq int, content map[string]any, txnID string) {
		_ = turnID
		_ = seq
		_ = txnID
		part, _ := content["part"].(map[string]any)
		typ, _ := part["type"].(string)
		got = append(got, typ)
	}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	state := &streamingState{turnID: "turn_1"}
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
	if len(got) < 3 {
		t.Fatalf("expected >=3 parts, got %v", got)
	}
	if got[0] != "tool-input-start" || got[1] != "tool-input-available" || got[2] != "tool-output-available" {
		t.Fatalf("unexpected part types: %v", got)
	}
}

func TestCodex_Mapping_ContextCompaction_EmitsToolParts(t *testing.T) {
	cc := &CodexClient{}
	var got []string
	cc.streamEventHook = func(turnID string, seq int, content map[string]any, txnID string) {
		_ = turnID
		_ = seq
		_ = txnID
		part, _ := content["part"].(map[string]any)
		typ, _ := part["type"].(string)
		got = append(got, typ)
	}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	state := &streamingState{turnID: "turn_1"}
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
	if len(got) < 3 {
		t.Fatalf("expected >=3 parts, got %v", got)
	}
	if got[0] != "tool-input-start" || got[1] != "tool-input-available" || got[2] != "tool-output-available" {
		t.Fatalf("unexpected part types: %v", got)
	}
}

func TestCodex_Mapping_ReviewMode_EmitsReviewToolOutput(t *testing.T) {
	cc := &CodexClient{}
	var gotTypes []string
	cc.streamEventHook = func(turnID string, seq int, content map[string]any, txnID string) {
		_ = turnID
		_ = seq
		_ = txnID
		part, _ := content["part"].(map[string]any)
		typ, _ := part["type"].(string)
		gotTypes = append(gotTypes, typ)
	}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	state := &streamingState{turnID: "turn_1"}
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

	// At least one tool output should be present.
	seenOutput := false
	for _, typ := range gotTypes {
		if typ == "tool-output-available" {
			seenOutput = true
			break
		}
	}
	if !seenOutput {
		t.Fatalf("expected tool-output-available, got %v", gotTypes)
	}
}
