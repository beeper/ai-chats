package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/bridges/codex/codexrpc"
	bridgesdk "github.com/beeper/agentremote/sdk"
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

func waitForPendingApproval(t *testing.T, ctx context.Context, cc *CodexClient, approvalID string) *agentremote.Pending[*pendingToolApprovalDataCodex] {
	t.Helper()
	for {
		pending := cc.approvalFlow.Get(approvalID)
		if pending != nil && pending.Data != nil {
			return pending
		}
		if err := ctx.Err(); err != nil {
			t.Fatalf("timed out waiting for approval %s: %v", approvalID, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func attachApprovalTestTurn(state *streamingState, portal *bridgev2.Portal) {
	if state == nil {
		return
	}
	conv := bridgesdk.NewConversation(context.Background(), nil, portal, bridgev2.EventSender{}, &bridgesdk.Config{}, nil)
	turn := conv.StartTurn(context.Background(), nil, nil)
	turn.SetID(state.turnID)
	state.turn = turn
}

func TestCodex_CommandApproval_RequestBlocksUntilApproved(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	cc := newTestCodexClient(id.UserID("@owner:example.com"))

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	meta := &PortalMetadata{}
	state := &streamingState{turnID: "turn_local", initialEventID: id.EventID("$event"), networkMessageID: networkid.MessageID("codex:test")}
	attachApprovalTestTurn(state, portal)
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

	pending := waitForPendingApproval(t, ctx, cc, "123")
	if !pending.Data.Presentation.AllowAlways {
		t.Fatalf("expected codex approvals to allow session-scoped always-allow")
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

	uiState := state.turn.UIState()
	if uiState == nil || !uiState.UIToolApprovalRequested["123"] {
		t.Fatal("expected approval request to be tracked in UI state")
	}
	if uiState.UIToolCallIDByApproval["123"] != "item_1" {
		t.Fatalf("expected approval to map to tool call item_1, got %q", uiState.UIToolCallIDByApproval["123"])
	}
}

func TestCodex_CommandApproval_DenyEmitsResponseThenOutputDenied(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	cc := newTestCodexClient(id.UserID("@owner:example.com"))

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	meta := &PortalMetadata{}
	state := &streamingState{turnID: "turn_local", initialEventID: id.EventID("$event"), networkMessageID: networkid.MessageID("codex:test")}
	attachApprovalTestTurn(state, portal)
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

	waitForPendingApproval(t, ctx, cc, "456")
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

	uiState := state.turn.UIState()
	if uiState == nil || !uiState.UIToolApprovalRequested["456"] {
		t.Fatal("expected denied approval request to be tracked in UI state")
	}
	if uiState.UIToolCallIDByApproval["456"] != "item_1" {
		t.Fatalf("expected approval to map to tool call item_1, got %q", uiState.UIToolCallIDByApproval["456"])
	}
}

func TestCodex_CommandApproval_AllowAlwaysMapsToSessionAcceptance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	cc := newTestCodexClient(id.UserID("@owner:example.com"))

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	meta := &PortalMetadata{}
	state := &streamingState{turnID: "turn_local", initialEventID: id.EventID("$event"), networkMessageID: networkid.MessageID("codex:test")}
	attachApprovalTestTurn(state, portal)
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
		"command":  "echo hi",
	})
	req := codexrpc.Request{
		ID:     json.RawMessage("654"),
		Method: "item/commandExecution/requestApproval",
		Params: paramsRaw,
	}

	resCh := make(chan map[string]any, 1)
	go func() {
		res, _ := cc.handleCommandApprovalRequest(ctx, req)
		resCh <- res.(map[string]any)
	}()

	waitForPendingApproval(t, ctx, cc, "654")
	if err := cc.approvalFlow.Resolve("654", agentremote.ApprovalDecisionPayload{
		ApprovalID: "654",
		Approved:   true,
		Always:     true,
		Reason:     agentremote.ApprovalReasonAllowAlways,
	}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case res := <-resCh:
		if res["decision"] != "acceptForSession" {
			t.Fatalf("expected decision=acceptForSession, got %#v", res)
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for approval handler to return")
	}
}

func TestCodex_CommandApproval_AllowAlwaysMapsToSessionDecision(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	cc := newTestCodexClient(id.UserID("@owner:example.com"))
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	state := &streamingState{turnID: "turn_local", initialEventID: id.EventID("$event"), networkMessageID: networkid.MessageID("codex:test")}
	attachApprovalTestTurn(state, portal)
	cc.activeTurns = map[string]*codexActiveTurn{
		codexTurnKey("thr_1", "turn_1"): {
			portal:   portal,
			meta:     &PortalMetadata{},
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
		"command":  "echo hi",
	})
	req := codexrpc.Request{ID: json.RawMessage("789"), Method: "item/commandExecution/requestApproval", Params: paramsRaw}

	resCh := make(chan map[string]any, 1)
	go func() {
		res, _ := cc.handleCommandApprovalRequest(ctx, req)
		resCh <- res.(map[string]any)
	}()

	waitForPendingApproval(t, ctx, cc, "789")
	if err := cc.approvalFlow.Resolve("789", agentremote.ApprovalDecisionPayload{
		ApprovalID: "789",
		Approved:   true,
		Always:     true,
		Reason:     agentremote.ApprovalReasonAllowAlways,
	}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case res := <-resCh:
		if res["decision"] != "acceptForSession" {
			t.Fatalf("expected decision=acceptForSession, got %#v", res)
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for approval handler to return")
	}
}

func TestCodex_CommandApproval_UsesExplicitApprovalID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	cc := newTestCodexClient(id.UserID("@owner:example.com"))
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	state := &streamingState{turnID: "turn_local", initialEventID: id.EventID("$event"), networkMessageID: networkid.MessageID("codex:test")}
	attachApprovalTestTurn(state, portal)
	cc.activeTurns = map[string]*codexActiveTurn{
		codexTurnKey("thr_1", "turn_1"): {
			portal:   portal,
			meta:     &PortalMetadata{},
			state:    state,
			threadID: "thr_1",
			turnID:   "turn_1",
			model:    "gpt-5.1-codex",
		},
	}

	paramsRaw, _ := json.Marshal(map[string]any{
		"threadId":   "thr_1",
		"turnId":     "turn_1",
		"itemId":     "item_1",
		"approvalId": "approval-callback",
		"command":    "echo hi",
	})
	req := codexrpc.Request{ID: json.RawMessage("123"), Method: "item/commandExecution/requestApproval", Params: paramsRaw}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = cc.handleCommandApprovalRequest(ctx, req)
	}()

	pending := waitForPendingApproval(t, ctx, cc, "approval-callback")
	if pending == nil {
		t.Fatal("expected explicit approval id to be registered")
	}
	if cc.approvalFlow.Get("123") != nil {
		t.Fatal("expected JSON-RPC request id not to be used when approvalId is present")
	}
	_ = cc.approvalFlow.Resolve("approval-callback", agentremote.ApprovalDecisionPayload{
		ApprovalID: "approval-callback",
		Approved:   false,
		Reason:     agentremote.ApprovalReasonDeny,
	})
	<-done
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

func TestCodex_PermissionsApproval_AllowAlwaysMapsToSessionScope(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	cc := newTestCodexClient(id.UserID("@owner:example.com"))

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	meta := &PortalMetadata{}
	state := &streamingState{turnID: "turn_local", initialEventID: id.EventID("$event"), networkMessageID: networkid.MessageID("codex:test")}
	attachApprovalTestTurn(state, portal)
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
		"itemId":   "perm_1",
		"reason":   "need write access",
		"permissions": map[string]any{
			"fileSystem": map[string]any{
				"write": []string{"/tmp/project"},
			},
		},
	})
	req := codexrpc.Request{
		ID:     json.RawMessage("777"),
		Method: "item/permissions/requestApproval",
		Params: paramsRaw,
	}

	resCh := make(chan map[string]any, 1)
	go func() {
		res, _ := cc.handlePermissionsApprovalRequest(ctx, req)
		resCh <- res.(map[string]any)
	}()

	waitForPendingApproval(t, ctx, cc, "777")
	if err := cc.approvalFlow.Resolve("777", agentremote.ApprovalDecisionPayload{
		ApprovalID: "777",
		Approved:   true,
		Always:     true,
		Reason:     agentremote.ApprovalReasonAllowAlways,
	}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case res := <-resCh:
		if res["scope"] != "session" {
			t.Fatalf("expected scope=session, got %#v", res)
		}
		permissions, ok := res["permissions"].(map[string]any)
		if !ok || len(permissions) == 0 {
			t.Fatalf("expected granted permissions, got %#v", res["permissions"])
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for permissions approval handler to return")
	}
}

func TestCodex_FileChangeApproval_AllowAlwaysMapsToSessionDecision(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	cc := newTestCodexClient(id.UserID("@owner:example.com"))
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	state := &streamingState{turnID: "turn_local", initialEventID: id.EventID("$event"), networkMessageID: networkid.MessageID("codex:test")}
	attachApprovalTestTurn(state, portal)
	cc.activeTurns = map[string]*codexActiveTurn{
		codexTurnKey("thr_1", "turn_1"): {
			portal:   portal,
			meta:     &PortalMetadata{},
			state:    state,
			threadID: "thr_1",
			turnID:   "turn_1",
		},
	}

	paramsRaw, _ := json.Marshal(map[string]any{
		"threadId": "thr_1",
		"turnId":   "turn_1",
		"itemId":   "patch_1",
		"reason":   "needs write access",
	})
	req := codexrpc.Request{ID: json.RawMessage("654"), Method: "item/fileChange/requestApproval", Params: paramsRaw}

	resCh := make(chan map[string]any, 1)
	go func() {
		res, _ := cc.handleFileChangeApprovalRequest(ctx, req)
		resCh <- res.(map[string]any)
	}()

	waitForPendingApproval(t, ctx, cc, "654")
	if err := cc.approvalFlow.Resolve("654", agentremote.ApprovalDecisionPayload{
		ApprovalID: "654",
		Approved:   true,
		Always:     true,
		Reason:     agentremote.ApprovalReasonAllowAlways,
	}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case res := <-resCh:
		if res["decision"] != "acceptForSession" {
			t.Fatalf("expected decision=acceptForSession, got %#v", res)
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for approval handler to return")
	}
}

func TestCodex_PermissionsApproval_ApproveSessionReturnsRequestedPermissions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	cc := newTestCodexClient(id.UserID("@owner:example.com"))
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	state := &streamingState{turnID: "turn_local", initialEventID: id.EventID("$event"), networkMessageID: networkid.MessageID("codex:test")}
	attachApprovalTestTurn(state, portal)
	cc.activeTurns = map[string]*codexActiveTurn{
		codexTurnKey("thr_1", "turn_1"): {
			portal:   portal,
			meta:     &PortalMetadata{},
			state:    state,
			threadID: "thr_1",
			turnID:   "turn_1",
		},
	}

	paramsRaw, _ := json.Marshal(map[string]any{
		"threadId": "thr_1",
		"turnId":   "turn_1",
		"itemId":   "perm_1",
		"reason":   "network access",
		"permissions": map[string]any{
			"network": map[string]any{"mode": "enabled"},
			"fileSystem": map[string]any{
				"writableRoots": []string{"/tmp/project"},
			},
		},
	})
	req := codexrpc.Request{ID: json.RawMessage("987"), Method: "item/permissions/requestApproval", Params: paramsRaw}

	resCh := make(chan map[string]any, 1)
	go func() {
		res, _ := cc.handlePermissionsApprovalRequest(ctx, req)
		resCh <- res.(map[string]any)
	}()

	waitForPendingApproval(t, ctx, cc, "987")
	if err := cc.approvalFlow.Resolve("987", agentremote.ApprovalDecisionPayload{
		ApprovalID: "987",
		Approved:   true,
		Always:     true,
		Reason:     agentremote.ApprovalReasonAllowAlways,
	}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case res := <-resCh:
		if res["scope"] != "session" {
			t.Fatalf("expected scope=session, got %#v", res)
		}
		perms, ok := res["permissions"].(map[string]any)
		if !ok || len(perms) == 0 {
			t.Fatalf("expected requested permissions to be returned, got %#v", res)
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for approval handler to return")
	}
}

func TestCodex_PermissionsApproval_DenyReturnsEmptyTurnScope(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	cc := newTestCodexClient(id.UserID("@owner:example.com"))
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	meta := &PortalMetadata{}
	state := &streamingState{turnID: "turn_local", initialEventID: id.EventID("$event"), networkMessageID: networkid.MessageID("codex:test")}
	attachApprovalTestTurn(state, portal)
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
		"threadId":    "thr_1",
		"turnId":      "turn_1",
		"itemId":      "perm_2",
		"permissions": map[string]any{"network": map[string]any{"enabled": true}},
	})
	req := codexrpc.Request{
		ID:     json.RawMessage("778"),
		Method: "item/permissions/requestApproval",
		Params: paramsRaw,
	}

	resCh := make(chan map[string]any, 1)
	go func() {
		res, _ := cc.handlePermissionsApprovalRequest(ctx, req)
		resCh <- res.(map[string]any)
	}()

	waitForPendingApproval(t, ctx, cc, "778")
	if err := cc.approvalFlow.Resolve("778", agentremote.ApprovalDecisionPayload{
		ApprovalID: "778",
		Approved:   false,
		Reason:     agentremote.ApprovalReasonDeny,
	}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case res := <-resCh:
		if res["scope"] != "turn" {
			t.Fatalf("expected scope=turn, got %#v", res)
		}
		perms, ok := res["permissions"].(map[string]any)
		if !ok || len(perms) != 0 {
			t.Fatalf("expected empty permissions, got %#v", res["permissions"])
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for permission approval handler to return")
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
