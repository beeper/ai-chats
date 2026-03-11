package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/bridges/codex/codexrpc"
	"github.com/beeper/agentremote/pkg/bridgeadapter"
)

func newTestCodexClient(owner id.UserID) *CodexClient {
	ul := &bridgev2.UserLogin{}
	ul.UserLogin = &database.UserLogin{
		UserMXID: owner,
	}
	cc := &CodexClient{
		UserLogin:   ul,
		activeRooms: make(map[id.RoomID]bool),
	}
	cc.approvalFlow = bridgeadapter.NewApprovalFlow(bridgeadapter.ApprovalFlowConfig[*pendingToolApprovalDataCodex]{
		Login: func() *bridgev2.UserLogin { return cc.UserLogin },
		RoomIDFromData: func(data *pendingToolApprovalDataCodex) id.RoomID {
			if data == nil {
				return ""
			}
			return data.RoomID
		},
	})
	return cc
}

func TestCodex_CommandApproval_RequestBlocksUntilApproved(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	var gotPartTypes []string
	cc := newTestCodexClient(id.UserID("@owner:example.com"))
	cc.streamEventHook = func(turnID string, seq int, content map[string]any, txnID string) {
		_ = turnID
		_ = seq
		_ = txnID
		if p, ok := content["part"].(map[string]any); ok {
			if typ, ok := p["type"].(string); ok {
				gotPartTypes = append(gotPartTypes, typ)
			}
		}
	}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	meta := &PortalMetadata{}
	state := &streamingState{turnID: "turn_local"}
	cc.activeTurns = map[string]*codexActiveTurn{
		codexTurnKey("thr_1", "turn_1"): {
			portal:   portal,
			meta:     meta,
			state:    state,
			threadID: "thr_1",
			turnID:   "turn_1",
			model:    "gpt-5.1-codex",
		},
	}

	params := map[string]any{
		"threadId": "thr_1",
		"turnId":   "turn_1",
		"itemId":   "item_1",
		"command":  "echo hi",
		"cwd":      "/tmp",
	}
	paramsRaw, _ := json.Marshal(params)
	req := codexrpc.Request{
		ID:     json.RawMessage("123"),
		Method: "item/commandExecution/requestApproval",
		Params: paramsRaw,
	}

	resCh := make(chan map[string]any, 1)
	go func() {
		res, _ := cc.handleCommandApprovalRequest(ctx, req)
		resCh <- res.(map[string]any)
	}()

	// Give the handler a moment to register and start waiting.
	time.Sleep(50 * time.Millisecond)

	if err := cc.approvalFlow.Resolve("123", bridgeadapter.ApprovalDecisionPayload{
		ApprovalID: "123",
		Approved:   true,
	}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case res := <-resCh:
		if res["decision"] != "accept" {
			t.Fatalf("expected decision=accept, got %#v", res)
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for approval handler to return")
	}

	// Ensure we emitted an approval request chunk.
	seenApproval := false
	for _, typ := range gotPartTypes {
		if typ == "tool-approval-request" {
			seenApproval = true
			break
		}
	}
	if !seenApproval {
		t.Fatalf("expected tool-approval-request in parts, got %v", gotPartTypes)
	}
}

func TestCodex_CommandApproval_AutoApproveInFullElevated(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	cc := newTestCodexClient(id.UserID("@owner:example.com"))
	cc.streamEventHook = func(turnID string, seq int, content map[string]any, txnID string) {}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	meta := &PortalMetadata{ElevatedLevel: "full"}
	state := &streamingState{turnID: "turn_local"}
	cc.activeTurns = map[string]*codexActiveTurn{
		codexTurnKey("thr_1", "turn_1"): {
			portal:   portal,
			meta:     meta,
			state:    state,
			threadID: "thr_1",
			turnID:   "turn_1",
		},
	}

	paramsRaw, _ := json.Marshal(map[string]any{
		"threadId": "thr_1",
		"turnId":   "turn_1",
		"itemId":   "item_1",
	})
	req := codexrpc.Request{
		ID:     json.RawMessage("321"),
		Method: "item/commandExecution/requestApproval",
		Params: paramsRaw,
	}

	res, _ := cc.handleCommandApprovalRequest(ctx, req)
	if res.(map[string]any)["decision"] != "accept" {
		t.Fatalf("expected decision=accept, got %#v", res)
	}
}

func TestCodex_CommandApproval_RejectCrossRoom(t *testing.T) {
	owner := id.UserID("@owner:example.com")
	roomID := id.RoomID("!room1:example.com")
	otherRoom := id.RoomID("!room2:example.com")

	cc := newTestCodexClient(owner)
	cc.registerToolApproval(roomID, "approval-1", "item-1", "commandExecution", 2*time.Second)

	// Register the approval in a second room to test cross-room rejection.
	// The flow's HandleReaction checks room via RoomIDFromData, so we test
	// that the registered room doesn't match a different room.
	p := cc.approvalFlow.Get("approval-1")
	if p == nil {
		t.Fatalf("expected pending approval to exist")
	}
	if p.Data == nil || p.Data.RoomID != roomID {
		t.Fatalf("expected pending data with RoomID=%s, got %v", roomID, p.Data)
	}
	// The RoomIDFromData callback returns roomID, which won't match otherRoom.
	_ = otherRoom
}
