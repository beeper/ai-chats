package openclaw

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	if strings.Contains(gotPath, "%253A") {
		t.Fatalf("session path was double-escaped: %q", gotPath)
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

func TestSessionHistoryFallsBackToChatHistoryRPC(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html>control-ui</html>"))
	}))
	defer server.Close()

	client := newGatewayWSClient(gatewayConnectConfig{URL: server.URL})
	client.hello = &gatewayHello{
		Features: gatewayHelloFeatures{Methods: []string{"chat.history"}},
	}
	client.requestFn = func(ctx context.Context, method string, params map[string]any, out any) error {
		if method != "chat.history" {
			t.Fatalf("unexpected method %q", method)
		}
		resp, ok := out.(*gatewaySessionHistoryResponse)
		if !ok {
			t.Fatalf("unexpected response type %T", out)
		}
		*resp = gatewaySessionHistoryResponse{
			Messages: []map[string]any{
				{"role": "assistant", "text": "one", "__openclaw": map[string]any{"seq": 1}},
				{"role": "assistant", "text": "two", "__openclaw": map[string]any{"seq": 2}},
				{"role": "assistant", "text": "three", "__openclaw": map[string]any{"seq": 3}},
			},
		}
		return nil
	}

	history, err := client.SessionHistory(context.Background(), "agent:main:test", 2, "4")
	if err != nil {
		t.Fatalf("SessionHistory returned error: %v", err)
	}
	if history == nil || len(history.Messages) != 2 {
		t.Fatalf("expected paginated rpc fallback history, got %#v", history)
	}
	if got := history.Messages[0]["text"]; got != "two" {
		t.Fatalf("unexpected first fallback message: %v", got)
	}
	if got := history.Messages[1]["text"]; got != "three" {
		t.Fatalf("unexpected second fallback message: %v", got)
	}
	if !history.HasMore || history.NextCursor != "2" {
		t.Fatalf("expected local pagination markers, got %#v", history)
	}
}

func TestProbeSessionHistoryAcceptsSemanticNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"type":"not_found","message":"missing"}}`))
	}))
	defer server.Close()

	client := newGatewayWSClient(gatewayConnectConfig{URL: server.URL})
	report := client.ProbeSessionHistory(context.Background())
	if !report.HistoryEndpointOK {
		t.Fatalf("expected semantic not_found to be accepted, got %#v", report)
	}
	if report.HistoryEndpointCode != http.StatusNotFound {
		t.Fatalf("unexpected history probe status: %d", report.HistoryEndpointCode)
	}
}

func TestProbeSessionHistoryRejectsGeneric404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"messages":[]}`))
	}))
	defer server.Close()

	client := newGatewayWSClient(gatewayConnectConfig{URL: server.URL})
	report := client.ProbeSessionHistory(context.Background())
	if report.HistoryEndpointOK {
		t.Fatalf("expected generic 404 to be rejected, got %#v", report)
	}
	if report.HistoryEndpointCode != http.StatusNotFound {
		t.Fatalf("unexpected history probe status: %d", report.HistoryEndpointCode)
	}
}

func TestProbeSessionHistoryAcceptsRPCFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html>control-ui</html>"))
	}))
	defer server.Close()

	client := newGatewayWSClient(gatewayConnectConfig{URL: server.URL})
	client.hello = &gatewayHello{
		Features: gatewayHelloFeatures{Methods: []string{"chat.history"}},
	}
	report := client.ProbeSessionHistory(context.Background())
	if !report.HistoryEndpointOK {
		t.Fatalf("expected rpc fallback probe to be accepted, got %#v", report)
	}
	if !strings.Contains(report.HistoryEndpointError, "invalid character '<'") {
		t.Fatalf("expected original http failure to be preserved, got %#v", report)
	}
}

func TestRequestUsesOverrideWhenProvided(t *testing.T) {
	client := newGatewayWSClient(gatewayConnectConfig{})
	client.requestFn = func(ctx context.Context, method string, params map[string]any, out any) error {
		if method != "models.list" {
			t.Fatalf("unexpected method %q", method)
		}
		resp, ok := out.(*gatewayModelsListResponse)
		if !ok {
			t.Fatalf("unexpected out type %T", out)
		}
		resp.Models = []gatewayModelChoice{{ID: "model-1"}}
		return nil
	}

	var resp gatewayModelsListResponse
	if err := client.Request(context.Background(), "models.list", nil, &resp); err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	if len(resp.Models) != 1 || resp.Models[0].ID != "model-1" {
		t.Fatalf("unexpected request override response: %#v", resp)
	}
}

func TestSessionHistoryReturnsCombinedFallbackErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html>control-ui</html>"))
	}))
	defer server.Close()

	client := newGatewayWSClient(gatewayConnectConfig{URL: server.URL})
	client.hello = &gatewayHello{
		Features: gatewayHelloFeatures{Methods: []string{"chat.history"}},
	}
	client.requestFn = func(ctx context.Context, method string, params map[string]any, out any) error {
		return errors.New("rpc unavailable")
	}

	_, err := client.SessionHistory(context.Background(), "agent:main:test", 10, "")
	if err == nil || !strings.Contains(err.Error(), "chat.history fallback failed") {
		t.Fatalf("expected combined fallback error, got %v", err)
	}
}
