package openclaw

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
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

func TestBuildPatchSessionParamsFlattensPatchFields(t *testing.T) {
	params := buildPatchSessionParams("session-1", map[string]any{
		"thinkingLevel": "medium",
		"fastMode":      true,
	})

	if got := params["key"]; got != "session-1" {
		t.Fatalf("unexpected key: %v", got)
	}
	if got := params["thinkingLevel"]; got != "medium" {
		t.Fatalf("unexpected thinkingLevel: %v", got)
	}
	if got := params["fastMode"]; got != true {
		t.Fatalf("unexpected fastMode: %v", got)
	}
	if _, exists := params["patch"]; exists {
		t.Fatalf("patch field should not be nested: %#v", params)
	}
}

func TestBuildPatchSessionParamsReservesMethodKey(t *testing.T) {
	params := buildPatchSessionParams(" session-1 ", map[string]any{
		"key":           "overridden",
		"thinkingLevel": "medium",
	})

	if got := params["key"]; got != "session-1" {
		t.Fatalf("expected method key to win, got %v", got)
	}
	if got := params["thinkingLevel"]; got != "medium" {
		t.Fatalf("unexpected thinkingLevel: %v", got)
	}
}

func TestApplyHelloPayloadPersistsDeviceToken(t *testing.T) {
	client := newGatewayWSClient(gatewayConnectConfig{})
	payload := json.RawMessage(`{"type":"hello-ok","auth":{"deviceToken":"persist-me"}}`)

	deviceToken := client.applyHelloPayload(payload, nil)
	if deviceToken != "persist-me" {
		t.Fatalf("expected device token from hello payload, got %q", deviceToken)
	}
	if got := client.cfg.DeviceToken; got != "persist-me" {
		t.Fatalf("expected client config to persist device token, got %q", got)
	}
}

func TestSessionHistoryUsesHTTPEndpointAndBearerAuth(t *testing.T) {
	var gotAuth string
	var gotPath string
	var gotLimit string
	var gotCursor string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotLimit = r.URL.Query().Get("limit")
		gotCursor = r.URL.Query().Get("cursor")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sessionKey":"agent:main:test","messages":[{"role":"assistant","__openclaw":{"seq":4}}],"nextCursor":"3","hasMore":true}`))
	}))
	defer server.Close()

	client := newGatewayWSClient(gatewayConnectConfig{
		URL:         strings.Replace(server.URL, "http://", "ws://", 1),
		Token:       "shared-token",
		DeviceToken: "device-token",
	})
	history, err := client.SessionHistory(context.Background(), "agent:main:test", 25, "seq:9")
	if err != nil {
		t.Fatalf("SessionHistory returned error: %v", err)
	}
	if gotAuth != "Bearer shared-token" {
		t.Fatalf("unexpected auth header: %q", gotAuth)
	}
	if gotPath != "/sessions/agent%3Amain%3Atest/history" && gotPath != "/sessions/agent:main:test/history" {
		t.Fatalf("unexpected path: %q", gotPath)
	}
	if gotLimit != "25" {
		t.Fatalf("unexpected limit: %q", gotLimit)
	}
	if gotCursor != "seq:9" {
		t.Fatalf("unexpected cursor: %q", gotCursor)
	}
	if history == nil || len(history.Messages) != 1 || history.NextCursor != "3" || !history.HasMore {
		t.Fatalf("unexpected history response: %#v", history)
	}
}

func TestSessionHistoryFallsBackToItemsArray(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sessionKey":"agent:main:test","items":[{"role":"assistant","text":"hello"}],"hasMore":false}`))
	}))
	defer server.Close()

	client := newGatewayWSClient(gatewayConnectConfig{URL: server.URL})
	history, err := client.SessionHistory(context.Background(), "agent:main:test", 0, "")
	if err != nil {
		t.Fatalf("SessionHistory returned error: %v", err)
	}
	if history == nil || len(history.Messages) != 1 {
		t.Fatalf("expected items to populate messages: %#v", history)
	}
}
