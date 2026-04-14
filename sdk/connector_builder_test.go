package sdk

import (
	"context"
	"errors"
	"sync"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

func TestConnectorBaseHookOrder(t *testing.T) {
	var order []string
	wantBridge := &bridgev2.Bridge{}
	conn := NewConnector(ConnectorSpec{
		Init: func(got *bridgev2.Bridge) {
			if got != wantBridge {
				t.Fatalf("expected init hook bridge %p, got %p", wantBridge, got)
			}
			order = append(order, "init")
		},
		Start: func(_ context.Context, got *bridgev2.Bridge) error {
			if got != wantBridge {
				t.Fatalf("expected start hook bridge %p, got %p", wantBridge, got)
			}
			order = append(order, "start")
			return nil
		},
		Stop: func(_ context.Context, got *bridgev2.Bridge) {
			if got != wantBridge {
				t.Fatalf("expected stop hook bridge %p, got %p", wantBridge, got)
			}
			order = append(order, "stop")
		},
	})
	conn.Init(wantBridge)
	if err := conn.Start(context.Background()); err != nil {
		t.Fatalf("start returned error: %v", err)
	}
	conn.Stop(context.Background())
	want := []string{"init", "start", "stop"}
	for i, step := range want {
		if len(order) <= i || order[i] != step {
			t.Fatalf("expected order %v, got %v", want, order)
		}
	}
}

func TestConnectorBaseLoginFlowsAndCreation(t *testing.T) {
	expected := &fakeLoginProcess{}
	conn := NewConnector(ConnectorSpec{
		LoginFlows: func() []bridgev2.LoginFlow {
			return []bridgev2.LoginFlow{{ID: "flow"}}
		},
		CreateLogin: func(context.Context, *bridgev2.User, string) (bridgev2.LoginProcess, error) {
			return expected, nil
		},
	})
	flows := conn.GetLoginFlows()
	if len(flows) != 1 || flows[0].ID != "flow" {
		t.Fatalf("unexpected login flows: %#v", flows)
	}
	got, err := conn.CreateLogin(context.Background(), &bridgev2.User{}, "flow")
	if err != nil {
		t.Fatalf("create login returned error: %v", err)
	}
	if got != expected {
		t.Fatalf("expected %T, got %T", expected, got)
	}
}

func TestTypedClientLoaderReusesAndRebuilds(t *testing.T) {
	var mu sync.Mutex
	clients := map[networkid.UserLoginID]bridgev2.NetworkAPI{}
	created := 0
	reused := 0
	loader := func(_ context.Context, login *bridgev2.UserLogin) error {
		return LoadUserLogin(login, LoadUserLoginConfig[*fakeClient]{
			Accept:     func(*bridgev2.UserLogin) (bool, string) { return true, "" },
			Mu:         &mu,
			Clients:    clients,
			BridgeName: "fake",
			Update: func(c *fakeClient, _ *bridgev2.UserLogin) {
				reused++
			},
			Create: func(*bridgev2.UserLogin) (*fakeClient, error) {
				created++
				return &fakeClient{}, nil
			},
		})
	}
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: "same"}}
	if err := loader(context.Background(), login); err != nil {
		t.Fatalf("first load returned error: %v", err)
	}
	if err := loader(context.Background(), login); err != nil {
		t.Fatalf("second load returned error: %v", err)
	}
	if created != 1 {
		t.Fatalf("expected 1 create, got %d", created)
	}
	if reused == 0 {
		t.Fatalf("expected reuse callback to run")
	}

	clients[login.ID] = &fakeOtherClient{}
	if err := loader(context.Background(), login); err != nil {
		t.Fatalf("rebuild load returned error: %v", err)
	}
	if created != 2 {
		t.Fatalf("expected rebuild to create second client, got %d creates", created)
	}
}

func TestTypedClientLoaderAssignsBrokenLoginOnRejectedLogin(t *testing.T) {
	loader := func(_ context.Context, login *bridgev2.UserLogin) error {
		return LoadUserLogin(login, LoadUserLoginConfig[*fakeClient]{
			Accept: func(*bridgev2.UserLogin) (bool, string) {
				return false, "nope"
			},
		})
	}
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: "broken"}}
	if err := loader(context.Background(), login); err != nil {
		t.Fatalf("loader returned error: %v", err)
	}
	if _, ok := login.Client.(*BrokenLoginClient); !ok {
		t.Fatalf("expected broken login client, got %T", login.Client)
	}
}

func TestTypedClientLoaderUsesClientMapReferenceWhenInitialCacheIsNil(t *testing.T) {
	var mu sync.Mutex
	var clients map[networkid.UserLoginID]bridgev2.NetworkAPI
	EnsureClientMap(&mu, &clients)

	loader := func(_ context.Context, login *bridgev2.UserLogin) error {
		return LoadUserLogin(login, LoadUserLoginConfig[*fakeClient]{
			Accept:     func(*bridgev2.UserLogin) (bool, string) { return true, "" },
			Mu:         &mu,
			ClientsRef: &clients,
			BridgeName: "fake",
			Create: func(*bridgev2.UserLogin) (*fakeClient, error) {
				return &fakeClient{}, nil
			},
		})
	}
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: "login-ref"}}
	if err := loader(context.Background(), login); err != nil {
		t.Fatalf("loader returned error: %v", err)
	}
	if clients[login.ID] == nil {
		t.Fatalf("expected client to be cached through ClientsRef")
	}
}

func TestConnectorStopCanDisconnectCachedClients(t *testing.T) {
	var mu sync.Mutex
	clients := map[networkid.UserLoginID]bridgev2.NetworkAPI{
		"a": &fakeClient{},
		"b": &fakeClient{},
	}
	conn := NewConnector(ConnectorSpec{
		Stop: func(context.Context, *bridgev2.Bridge) {
			StopClients(&mu, &clients)
		},
	})
	conn.Stop(context.Background())
	for id, client := range clients {
		fc := client.(*fakeClient)
		if !fc.disconnected {
			t.Fatalf("expected client %s to disconnect", id)
		}
	}
}

func TestConnectorBaseDefaultsBridgeInfoAndCapabilities(t *testing.T) {
	conn := NewConnector(ConnectorSpec{ProtocolID: "ai-test"})
	caps := conn.GetCapabilities()
	if caps == nil {
		t.Fatalf("expected capabilities, got %#v", caps)
	}
	if caps.DisappearingMessages || caps.Provisioning.ResolveIdentifier.ContactList {
		t.Fatalf("expected empty default capabilities, got %#v", caps)
	}
	infoVer, capVer := conn.GetBridgeInfoVersion()
	wantInfo, wantCap := DefaultBridgeInfoVersion()
	if infoVer != wantInfo || capVer != wantCap {
		t.Fatalf("expected versions %d/%d, got %d/%d", wantInfo, wantCap, infoVer, capVer)
	}
	portal := &bridgev2.Portal{Portal: &database.Portal{RoomType: database.RoomTypeDM}}
	content := &event.BridgeEventContent{}
	conn.FillPortalBridgeInfo(portal, content)
	if content.Protocol.ID != "ai-test" {
		t.Fatalf("expected protocol id ai-test, got %q", content.Protocol.ID)
	}
	if content.BeeperRoomTypeV2 != "dm" {
		t.Fatalf("expected dm bridge room type, got %q", content.BeeperRoomTypeV2)
	}
}

type fakeClient struct {
	baseTestClient
	disconnected bool
}

func (c *fakeClient) Disconnect() { c.disconnected = true }

type fakeOtherClient struct{ fakeClient }

type fakeLoginProcess struct{}

func (*fakeLoginProcess) Start(context.Context) (*bridgev2.LoginStep, error) { return nil, nil }
func (*fakeLoginProcess) Cancel()                                            {}

var _ bridgev2.NetworkAPI = (*fakeClient)(nil)

func TestTypedClientLoaderPropagatesCreateErrorViaBrokenLogin(t *testing.T) {
	loader := func(_ context.Context, login *bridgev2.UserLogin) error {
		return LoadUserLogin(login, LoadUserLoginConfig[*fakeClient]{
			Accept:     func(*bridgev2.UserLogin) (bool, string) { return true, "" },
			BridgeName: "fake",
			Create: func(*bridgev2.UserLogin) (*fakeClient, error) {
				return nil, errors.New("boom")
			},
		})
	}
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: "broken-create"}}
	if err := loader(context.Background(), login); err != nil {
		t.Fatalf("loader returned error: %v", err)
	}
	if _, ok := login.Client.(*BrokenLoginClient); !ok {
		t.Fatalf("expected broken login after create failure, got %T", login.Client)
	}
}

func TestClientBaseTracksLogin(t *testing.T) {
	var base ClientBase
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: "user"}}
	base.SetUserLogin(login)
	if base.GetUserLogin() != login {
		t.Fatalf("expected stored login to match input")
	}
}

var (
	_ bridgev2.LoginProcess = (*fakeLoginProcess)(nil)
	_ bridgev2.NetworkAPI   = (*fakeOtherClient)(nil)
)
