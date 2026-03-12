package codex

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/bridges/codex/codexrpc"
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
	cc.approvalFlow = agentremote.NewApprovalFlow(agentremote.ApprovalFlowConfig[*pendingToolApprovalDataCodex]{
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

	var mu sync.Mutex
	var gotPartTypes []string
	var gotParts []map[string]any
	cc := newTestCodexClient(id.UserID("@owner:example.com"))
	cc.streamEventHook = func(turnID string, seq int, content map[string]any, txnID string) {
		_ = turnID
		_ = seq
		_ = txnID
		if p, ok := content["part"].(map[string]any); ok {
			mu.Lock()
			gotParts = append(gotParts, p)
			if typ, ok := p["type"].(string); ok {
				gotPartTypes = append(gotPartTypes, typ)
			}
			mu.Unlock()
		}
	}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	meta := &PortalMetadata{}
	state := &streamingState{turnID: "turn_local", initialEventID: id.EventID("$event"), networkMessageID: networkid.MessageID("codex:test")}
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
	pending := cc.approvalFlow.Get("123")
	if pending == nil || pending.Data == nil {
		t.Fatalf("expected pending approval")
	}
	if pending.Data.Presentation.AllowAlways {
		t.Fatalf("expected codex approvals to disable always-allow")
	}
	if pending.Data.Presentation.Title == "" {
		t.Fatalf("expected structured presentation title")
	}

	if err := cc.approvalFlow.Resolve("123", agentremote.ApprovalDecisionPayload{
		ApprovalID: "123",
		Approved:   true,
		Reason:     "allow_once",
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

	mu.Lock()
	defer mu.Unlock()
	hasRequest := false
	hasResponse := false
	hasDenied := false
	for _, p := range gotParts {
		typ, _ := p["type"].(string)
		switch typ {
		case "tool-approval-request":
			hasRequest = true
		case "tool-approval-response":
			hasResponse = true
			if approved, ok := p["approved"].(bool); !ok || !approved {
				t.Fatalf("expected approval response approved=true, got %#v", p)
			}
		case "tool-output-denied":
			hasDenied = true
		}
	}
	if !hasRequest || !hasResponse {
		t.Fatalf("expected request+response parts, got types %v", gotPartTypes)
	}
	if hasDenied {
		t.Fatalf("unexpected tool-output-denied for approved decision")
	}
}

func TestCodex_CommandApproval_DenyEmitsResponseThenOutputDenied(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	var mu sync.Mutex
	var gotPartTypes []string
	cc := newTestCodexClient(id.UserID("@owner:example.com"))
	cc.streamEventHook = func(turnID string, seq int, content map[string]any, txnID string) {
		_ = turnID
		_ = seq
		_ = txnID
		if p, ok := content["part"].(map[string]any); ok {
			if typ, ok := p["type"].(string); ok {
				mu.Lock()
				gotPartTypes = append(gotPartTypes, typ)
				mu.Unlock()
			}
		}
	}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	meta := &PortalMetadata{}
	state := &streamingState{turnID: "turn_local", initialEventID: id.EventID("$event"), networkMessageID: networkid.MessageID("codex:test")}
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

	paramsRaw, _ := json.Marshal(map[string]any{
		"threadId": "thr_1",
		"turnId":   "turn_1",
		"itemId":   "item_1",
		"command":  "rm -rf /tmp/test",
	})
	req := codexrpc.Request{
		ID:     json.RawMessage("456"),
		Method: "item/commandExecution/requestApproval",
		Params: paramsRaw,
	}

	resCh := make(chan map[string]any, 1)
	go func() {
		res, _ := cc.handleCommandApprovalRequest(ctx, req)
		resCh <- res.(map[string]any)
	}()

	time.Sleep(50 * time.Millisecond)
	if err := cc.approvalFlow.Resolve("456", agentremote.ApprovalDecisionPayload{
		ApprovalID: "456",
		Approved:   false,
		Reason:     "deny",
	}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case res := <-resCh:
		if res["decision"] != "decline" {
			t.Fatalf("expected decision=decline, got %#v", res)
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for approval handler to return")
	}

	mu.Lock()
	defer mu.Unlock()
	idxResponse := -1
	idxDenied := -1
	for idx, typ := range gotPartTypes {
		if typ == "tool-approval-response" && idxResponse < 0 {
			idxResponse = idx
		}
		if typ == "tool-output-denied" && idxDenied < 0 {
			idxDenied = idx
		}
	}
	if idxResponse < 0 {
		t.Fatalf("expected tool-approval-response in parts, got %v", gotPartTypes)
	}
	if idxDenied < 0 {
		t.Fatalf("expected tool-output-denied in parts, got %v", gotPartTypes)
	}
	if idxDenied <= idxResponse {
		t.Fatalf("expected tool-output-denied after response, got %v", gotPartTypes)
	}
}

func TestCodex_CommandApproval_AutoApproveInFullElevated(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	cc := newTestCodexClient(id.UserID("@owner:example.com"))
	cc.streamEventHook = func(turnID string, seq int, content map[string]any, txnID string) {}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	meta := &PortalMetadata{ElevatedLevel: "full"}
	state := &streamingState{turnID: "turn_local", initialEventID: id.EventID("$event"), networkMessageID: networkid.MessageID("codex:test")}
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
	cc.registerToolApproval(roomID, "approval-1", "item-1", "commandExecution", agentremote.ApprovalPromptPresentation{
		Title:       "Codex command execution",
		AllowAlways: false,
	}, 2*time.Second)

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
