package sdk

import (
	"context"
	"sync"
	"testing"

	"github.com/beeper/agentremote"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

type testSDKClient struct {
	updated int
}

func (c *testSDKClient) Connect(context.Context)                           {}
func (c *testSDKClient) Disconnect()                                       {}
func (c *testSDKClient) IsLoggedIn() bool                                  { return true }
func (c *testSDKClient) LogoutRemote(context.Context)                      {}
func (c *testSDKClient) IsThisUser(context.Context, networkid.UserID) bool { return false }
func (c *testSDKClient) GetChatInfo(context.Context, *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	return nil, nil
}
func (c *testSDKClient) GetUserInfo(context.Context, *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	return nil, nil
}
func (c *testSDKClient) GetCapabilities(context.Context, *bridgev2.Portal) *event.RoomFeatures {
	return &event.RoomFeatures{}
}
func (c *testSDKClient) HandleMatrixMessage(context.Context, *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	return nil, nil
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

	cfg := &Config{
		Name:          "hooked",
		ClientCacheMu: &mu,
		ClientCache:   &clients,
		AcceptLogin: func(login *bridgev2.UserLogin) (bool, string) {
			if login.ID == "blocked" {
				return false, "blocked"
			}
			return true, ""
		},
		InitConnector: func(*bridgev2.Bridge) { initCalled++ },
		StartConnector: func(context.Context, *bridgev2.Bridge) error {
			startCalled++
			return nil
		},
		StopConnector: func(context.Context, *bridgev2.Bridge) { stopCalled++ },
		MakeBrokenLogin: func(login *bridgev2.UserLogin, reason string) *agentremote.BrokenLoginClient {
			return agentremote.NewBrokenLoginClient(login, "custom:"+reason)
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
	conn.Init(nil)
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
	broken, ok := blocked.Client.(*agentremote.BrokenLoginClient)
	if !ok {
		t.Fatalf("expected broken login client, got %T", blocked.Client)
	}
	if broken.Reason != "custom:blocked" {
		t.Fatalf("unexpected broken reason: %q", broken.Reason)
	}
}

func TestTurnRequestApprovalUsesCustomRequester(t *testing.T) {
	conv := NewConversation(context.Background(), nil, nil, bridgev2.EventSender{}, &Config{}, nil)
	turn := conv.StartTurn(context.Background(), &Agent{ID: "agent"}, nil)

	called := false
	turn.SetApprovalRequester(func(_ context.Context, gotTurn *Turn, req ApprovalRequest) ApprovalHandle {
		called = true
		if gotTurn != turn {
			t.Fatalf("expected requester turn to match")
		}
		if req.ApprovalID != "approval-1" || req.ToolCallID != "tool-1" || req.ToolName != "search" {
			t.Fatalf("unexpected approval request: %#v", req)
		}
		return &testApprovalHandle{id: "approval-1", toolCallID: req.ToolCallID}
	})

	handle := turn.RequestApproval(ApprovalRequest{
		ApprovalID: "approval-1",
		ToolCallID: "tool-1",
		ToolName:   "search",
	})
	if !called {
		t.Fatal("expected custom approval requester to be called")
	}
	if handle.ID() != "approval-1" || handle.ToolCallID() != "tool-1" {
		t.Fatalf("unexpected handle: id=%q tool=%q", handle.ID(), handle.ToolCallID())
	}
}

var _ bridgev2.NetworkAPI = (*testSDKClient)(nil)
