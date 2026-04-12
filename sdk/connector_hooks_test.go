package sdk

import (
	"context"
	"sync"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

type testSDKClient struct {
	baseTestClient
	updated int
}

type testApprovalHandle struct {
	id         string
	toolCallID string
}

func (h *testApprovalHandle) ID() string { return h.id }

func (h *testApprovalHandle) ToolCallID() string { return h.toolCallID }

func (h *testApprovalHandle) Wait(context.Context) (ToolApprovalResponse, error) {
	return ToolApprovalResponse{Approved: true, Reason: "allow_once"}, nil
}

func TestNewConnectorBaseUsesHooksAndCustomClients(t *testing.T) {
	var mu sync.Mutex
	clients := map[networkid.UserLoginID]bridgev2.NetworkAPI{}
	initCalled := 0
	startCalled := 0
	stopCalled := 0
	createCalled := 0
	updateCalled := 0
	afterLoadCalled := 0
	wantBridge := &bridgev2.Bridge{}

	cfg := &Config[*struct{}, *struct{}]{
		Name:          "hooked",
		ClientCacheMu: &mu,
		ClientCache:   &clients,
		AcceptLogin: func(login *bridgev2.UserLogin) (bool, string) {
			if login.ID == "blocked" {
				return false, "blocked"
			}
			return true, ""
		},
		InitConnector: func(got *bridgev2.Bridge) {
			if got != wantBridge {
				t.Fatalf("expected init bridge %p, got %p", wantBridge, got)
			}
			initCalled++
		},
		StartConnector: func(_ context.Context, got *bridgev2.Bridge) error {
			if got != wantBridge {
				t.Fatalf("expected start bridge %p, got %p", wantBridge, got)
			}
			startCalled++
			return nil
		},
		StopConnector: func(_ context.Context, got *bridgev2.Bridge) {
			if got != wantBridge {
				t.Fatalf("expected stop bridge %p, got %p", wantBridge, got)
			}
			stopCalled++
		},
		MakeBrokenLogin: func(login *bridgev2.UserLogin, reason string) *BrokenLoginClient {
			return NewBrokenLoginClient(login, "custom:"+reason)
		},
		CreateClient: func(*bridgev2.UserLogin) (bridgev2.NetworkAPI, error) {
			createCalled++
			return &testSDKClient{}, nil
		},
		UpdateClient: func(client bridgev2.NetworkAPI, _ *bridgev2.UserLogin) {
			updateCalled++
			client.(*testSDKClient).updated++
		},
		AfterLoadClient: func(bridgev2.NetworkAPI) { afterLoadCalled++ },
	}

	conn := NewConnectorBase(cfg)
	conn.Init(wantBridge)
	if err := conn.Start(context.Background()); err != nil {
		t.Fatalf("start returned error: %v", err)
	}
	conn.Stop(context.Background())
	if initCalled != 1 || startCalled != 1 || stopCalled != 1 {
		t.Fatalf("unexpected hook counts: init=%d start=%d stop=%d", initCalled, startCalled, stopCalled)
	}

	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: "ok"}}
	if err := conn.LoadUserLogin(context.Background(), login); err != nil {
		t.Fatalf("load login returned error: %v", err)
	}
	if _, ok := login.Client.(*testSDKClient); !ok {
		t.Fatalf("expected testSDKClient, got %T", login.Client)
	}
	if createCalled != 1 || afterLoadCalled != 1 {
		t.Fatalf("unexpected create/after counts: create=%d after=%d", createCalled, afterLoadCalled)
	}

	if err := conn.LoadUserLogin(context.Background(), login); err != nil {
		t.Fatalf("reload login returned error: %v", err)
	}
	if updateCalled != 1 {
		t.Fatalf("expected update callback on reload, got %d", updateCalled)
	}

	blocked := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: "blocked"}}
	if err := conn.LoadUserLogin(context.Background(), blocked); err != nil {
		t.Fatalf("blocked login returned error: %v", err)
	}
	broken, ok := blocked.Client.(*BrokenLoginClient)
	if !ok {
		t.Fatalf("expected broken login client, got %T", blocked.Client)
	}
	if broken.Reason != "custom:blocked" {
		t.Fatalf("unexpected broken reason: %q", broken.Reason)
	}
}

func TestNewConnectorBaseUsesCustomLoadLoginAndLoginFlows(t *testing.T) {
	loadCalled := 0
	cfg := &Config[*struct{}, *struct{}]{
		Name: "custom-load",
		LoadLogin: func(_ context.Context, login *bridgev2.UserLogin) error {
			loadCalled++
			login.Client = &testSDKClient{}
			return nil
		},
		GetLoginFlows: func() []bridgev2.LoginFlow {
			return []bridgev2.LoginFlow{{
				ID:   "custom",
				Name: "Custom",
			}}
		},
		BridgeName: func() bridgev2.BridgeName {
			return bridgev2.BridgeName{
				DisplayName: "Custom Load",
				NetworkIcon: "mxc://icon",
			}
		},
	}

	conn := NewConnectorBase(cfg)
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: "ok"}}
	if err := conn.LoadUserLogin(context.Background(), login); err != nil {
		t.Fatalf("load login returned error: %v", err)
	}
	if loadCalled != 1 {
		t.Fatalf("expected custom load login to be called once, got %d", loadCalled)
	}
	if _, ok := login.Client.(*testSDKClient); !ok {
		t.Fatalf("expected custom load login to set testSDKClient, got %T", login.Client)
	}

	flows := conn.GetLoginFlows()
	if len(flows) != 1 || flows[0].ID != "custom" {
		t.Fatalf("unexpected login flows: %#v", flows)
	}
	if got := conn.GetName().NetworkIcon; got != "mxc://icon" {
		t.Fatalf("expected network icon to round-trip, got %q", got)
	}
}

func TestApprovalControllerUsesCustomHandler(t *testing.T) {
	conv := NewConversation(context.Background(), nil, nil, bridgev2.EventSender{}, &Config[*struct{}, *struct{}]{}, nil)
	turn := conv.StartTurn(context.Background(), &Agent{ID: "agent"}, nil)

	called := false
	turn.Approvals().SetHandler(func(_ context.Context, gotTurn *Turn, req ApprovalRequest) ApprovalHandle {
		called = true
		if gotTurn != turn {
			t.Fatalf("expected handler turn to match")
		}
		if req.ApprovalID != "approval-2" || req.ToolCallID != "tool-2" || req.ToolName != "shell" {
			t.Fatalf("unexpected approval request: %#v", req)
		}
		return &testApprovalHandle{id: "approval-2", toolCallID: req.ToolCallID}
	})

	handle := turn.Approvals().Request(ApprovalRequest{
		ApprovalID: "approval-2",
		ToolCallID: "tool-2",
		ToolName:   "shell",
	})
	if !called {
		t.Fatal("expected approval handler to be called")
	}
	if handle.ID() != "approval-2" || handle.ToolCallID() != "tool-2" {
		t.Fatalf("unexpected handle: id=%q tool=%q", handle.ID(), handle.ToolCallID())
	}
}

func TestResolveCommandPrefixTrimsConfiguredValue(t *testing.T) {
	if got := ResolveCommandPrefix(" /ai ", "!fallback"); got != "/ai" {
		t.Fatalf("expected trimmed configured prefix, got %q", got)
	}
	if got := ResolveCommandPrefix("   ", "!fallback"); got != "!fallback" {
		t.Fatalf("expected fallback prefix, got %q", got)
	}
}

var _ bridgev2.NetworkAPI = (*testSDKClient)(nil)
