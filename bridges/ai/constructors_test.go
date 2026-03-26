package ai

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote"
)

func TestNewAIConnectorUsesSDKConfig(t *testing.T) {
	conn := NewAIConnector()
	if conn.sdkConfig == nil {
		t.Fatal("expected sdkConfig to be initialized")
	}
	if conn.clients == nil {
		t.Fatal("expected client cache map to be initialized")
	}
	if conn.ConnectorBase == nil {
		t.Fatal("expected ConnectorBase to be initialized")
	}

	name := conn.GetName()
	if name.DisplayName != "Beeper Cloud" {
		t.Fatalf("unexpected display name %q", name.DisplayName)
	}
	if name.NetworkURL != "https://www.beeper.com/ai" {
		t.Fatalf("unexpected network url %q", name.NetworkURL)
	}
	if name.NetworkIcon != "mxc://beeper.com/51a668657dd9e0132cc823ad9402c6c2d0fc3321" {
		t.Fatalf("unexpected network icon %q", name.NetworkIcon)
	}
	if name.NetworkID != "ai" || name.BeeperBridgeType != "ai" {
		t.Fatalf("unexpected bridge identity: %#v", name)
	}
	if name.DefaultPort != 29345 {
		t.Fatalf("unexpected default port %d", name.DefaultPort)
	}
}

func TestNewAIConnectorInitializesClientCacheMap(t *testing.T) {
	conn := NewAIConnector()

	loginID := networkid.UserLoginID("login-1")
	conn.clients[loginID] = nil

	if _, ok := conn.clients[loginID]; !ok {
		t.Fatal("expected write to initialized client cache map to succeed")
	}
}

func TestNewAIConnectorLoginFlowsRemainDynamic(t *testing.T) {
	conn := NewAIConnector()

	flows := conn.GetLoginFlows()
	if len(flows) != 2 {
		t.Fatalf("expected Magic Proxy and Manual login flows, got %#v", flows)
	}
	if flows[0].ID != ProviderMagicProxy || flows[1].ID != FlowCustom {
		t.Fatalf("unexpected login flows: %#v", flows)
	}
}

func TestNewAIConnectorLoadLoginUsesCustomLoader(t *testing.T) {
	conn := NewAIConnector()
	conn.Init(nil)

	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: "login-1"}}
	if err := conn.LoadUserLogin(context.Background(), login); err != nil {
		t.Fatalf("load login returned error: %v", err)
	}
	if _, ok := login.Client.(*agentremote.BrokenLoginClient); !ok {
		t.Fatalf("expected broken login client for missing API key, got %T", login.Client)
	}
}
