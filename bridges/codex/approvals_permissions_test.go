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
	"github.com/beeper/agentremote/sdk"
)

func TestCodex_CommandApproval_AutoApproveInFullElevated(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	cc := newTestCodexClient(id.UserID("@owner:example.com"))
	cc.streamEventHook = func(turnID string, seq int, content map[string]any, txnID string) {}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	portalState := &codexPortalState{ElevatedLevel: "full"}
	state := &streamingState{turnID: "turn_local", initialEventID: id.EventID("$event")}
	cc.activeTurns = map[string]*codexActiveTurn{
		codexTurnKey("thr_1", "turn_1"): {
			portal:      portal,
			portalState: portalState,
			streamState: state,
			threadID:    "thr_1",
			turnID:      "turn_1",
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
	f := newApprovalTestFixture(t)
	ctx, cc := f.ctx, f.cc

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
	if err := cc.approvalFlow.Resolve("777", sdk.ApprovalDecisionPayload{
		ApprovalID: "777",
		Approved:   true,
		Always:     true,
		Reason:     sdk.ApprovalReasonAllowAlways,
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
	f := newApprovalTestFixture(t)
	ctx, cc := f.ctx, f.cc

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
	if err := cc.approvalFlow.Resolve("654", sdk.ApprovalDecisionPayload{
		ApprovalID: "654",
		Approved:   true,
		Always:     true,
		Reason:     sdk.ApprovalReasonAllowAlways,
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
	f := newApprovalTestFixture(t)
	ctx, cc := f.ctx, f.cc

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
	if err := cc.approvalFlow.Resolve("987", sdk.ApprovalDecisionPayload{
		ApprovalID: "987",
		Approved:   true,
		Always:     true,
		Reason:     sdk.ApprovalReasonAllowAlways,
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
	f := newApprovalTestFixture(t)
	ctx, cc := f.ctx, f.cc

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
	if err := cc.approvalFlow.Resolve("778", sdk.ApprovalDecisionPayload{
		ApprovalID: "778",
		Approved:   false,
		Reason:     sdk.ApprovalReasonDeny,
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
