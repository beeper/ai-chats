package openclaw

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"runtime"
	"testing"
)

func TestBuildConnectParamsUsesOperatorClientShape(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}

	client := newGatewayWSClient(gatewayConnectConfig{
		URL:         "ws://127.0.0.1:18789",
		Token:       "shared-token",
		DeviceToken: "device-token",
	})
	params, err := client.buildConnectParams(&gatewayDeviceIdentity{
		Version:    1,
		DeviceID:   "device-id",
		PublicKey:  base64.StdEncoding.EncodeToString(pub),
		PrivateKey: base64.StdEncoding.EncodeToString(priv),
	}, "nonce")
	if err != nil {
		t.Fatalf("buildConnectParams returned error: %v", err)
	}

	clientParams, ok := params["client"].(map[string]any)
	if !ok {
		t.Fatalf("expected client params map, got %#v", params["client"])
	}
	if got := clientParams["id"]; got != openClawGatewayClientID {
		t.Fatalf("unexpected client id: %v", got)
	}
	if got := clientParams["mode"]; got != openClawGatewayClientMode {
		t.Fatalf("unexpected client mode: %v", got)
	}
	if got := clientParams["platform"]; got != runtime.GOOS {
		t.Fatalf("unexpected client platform: %v", got)
	}
	if _, ok := clientParams["commands"]; ok {
		t.Fatalf("commands should not be nested in client params: %#v", clientParams)
	}

	auth, ok := params["auth"].(map[string]any)
	if !ok {
		t.Fatalf("expected auth params map, got %#v", params["auth"])
	}
	if got := auth["token"]; got != "shared-token" {
		t.Fatalf("expected shared token to stay in auth.token, got %v", got)
	}
	if got := auth["deviceToken"]; got != "device-token" {
		t.Fatalf("expected auth.deviceToken to be present, got %v", got)
	}
	if _, ok := params["commands"].([]string); !ok {
		t.Fatalf("expected top-level commands slice, got %#v", params["commands"])
	}
	if _, ok := params["permissions"].(map[string]bool); !ok {
		t.Fatalf("expected top-level permissions map, got %#v", params["permissions"])
	}
}

func TestGatewaySessionOriginStringParsesStructuredOrigin(t *testing.T) {
	var structured gatewaySessionsListResponse
	if err := json.Unmarshal([]byte(`{"sessions":[{"key":"k","kind":"direct","origin":{"label":"Support","provider":"slack","threadId":123}}]}`), &structured); err != nil {
		t.Fatalf("unmarshal structured response failed: %v", err)
	}
	if got := structured.Sessions[0].OriginString(); got != `{"label":"Support","provider":"slack","threadId":123}` {
		t.Fatalf("unexpected structured origin: %q", got)
	}
}
