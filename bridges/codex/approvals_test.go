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

type approvalTestFixture struct {
	ctx         context.Context
	cc          *CodexClient
	portal      *bridgev2.Portal
	portalState *codexPortalState
	streamState *streamingState
}

func newApprovalTestFixture(t *testing.T) approvalTestFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	cc := newTestCodexClient(id.UserID("@owner:example.com"))
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.com")}}
	portalState := &codexPortalState{}
	streamState := &streamingState{turnID: "turn_local", initialEventID: id.EventID("$event")}
	attachTestTurn(streamState, portal)
	cc.activeTurns = map[string]*codexActiveTurn{
		codexTurnKey("thr_1", "turn_1"): {
			portal:      portal,
			portalState: portalState,
			streamState: streamState,
			threadID:    "thr_1",
			turnID:      "turn_1",
			model:       "gpt-5.1-codex",
		},
	}
	return approvalTestFixture{ctx: ctx, cc: cc, portal: portal, portalState: portalState, streamState: streamState}
}

func newTestCodexClient(owner id.UserID) *CodexClient {
	ul := &bridgev2.UserLogin{}
	ul.UserLogin = &database.UserLogin{
		UserMXID: owner,
	}
	cc := &CodexClient{
		UserLogin:   ul,
		activeRooms: make(map[id.RoomID]bool),
	}
	cc.approvalFlow = sdk.NewApprovalFlow(sdk.ApprovalFlowConfig[*pendingToolApprovalDataCodex]{
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

func waitForPendingApproval(t *testing.T, ctx context.Context, cc *CodexClient, approvalID string) *sdk.Pending[*pendingToolApprovalDataCodex] {
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

func TestCodex_CommandApproval_RequestBlocksUntilApproved(t *testing.T) {
	f := newApprovalTestFixture(t)
	ctx, cc, state := f.ctx, f.cc, f.streamState

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

	if err := cc.approvalFlow.Resolve("123", sdk.ApprovalDecisionPayload{
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
	f := newApprovalTestFixture(t)
	ctx, cc, state := f.ctx, f.cc, f.streamState

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
	if err := cc.approvalFlow.Resolve("456", sdk.ApprovalDecisionPayload{
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
	f := newApprovalTestFixture(t)
	ctx, cc := f.ctx, f.cc

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

func TestCodex_CommandApproval_AllowAlwaysMapsToSessionDecision(t *testing.T) {
	f := newApprovalTestFixture(t)
	ctx, cc := f.ctx, f.cc

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
	if err := cc.approvalFlow.Resolve("789", sdk.ApprovalDecisionPayload{
		ApprovalID: "789",
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

func TestCodex_CommandApproval_UsesExplicitApprovalID(t *testing.T) {
	f := newApprovalTestFixture(t)
	ctx, cc := f.ctx, f.cc

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
	_ = cc.approvalFlow.Resolve("approval-callback", sdk.ApprovalDecisionPayload{
		ApprovalID: "approval-callback",
		Approved:   false,
		Reason:     sdk.ApprovalReasonDeny,
	})
	<-done
}
