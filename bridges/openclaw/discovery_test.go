package openclaw

import (
	"net/http/httptest"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"
)

func TestRegisterLoginPrefillIsUserScopedAndExpires(t *testing.T) {
	connector := &OpenClawConnector{
		Config: Config{
			OpenClaw: OpenClawConfig{
				Discovery: OpenClawDiscoveryConfig{
					PrefillTTLSeconds: 1,
				},
			},
		},
	}
	user := &bridgev2.User{User: &database.User{MXID: id.UserID("@alice:example.com")}}
	otherUser := &bridgev2.User{User: &database.User{MXID: id.UserID("@bob:example.com")}}

	flowID, expiresAt := connector.registerLoginPrefill(user, "wss://gateway.local:443", "Studio")
	if flowID == "" {
		t.Fatal("expected a generated flow id")
	}
	if expiresAt.IsZero() {
		t.Fatal("expected a non-zero expiry")
	}

	prefill, ok := connector.loginPrefill(flowID, user)
	if !ok {
		t.Fatal("expected prefill to be available for original user")
	}
	if prefill.URL != "wss://gateway.local:443" || prefill.Label != "Studio" {
		t.Fatalf("unexpected prefill: %#v", prefill)
	}
	if _, ok := connector.loginPrefill(flowID, otherUser); ok {
		t.Fatal("expected prefill lookup for another user to fail")
	}

	connector.prefillsMu.Lock()
	connector.prefills[flowID] = openClawLoginPrefill{
		UserMXID:  user.MXID,
		URL:       prefill.URL,
		Label:     prefill.Label,
		ExpiresAt: time.Now().Add(-time.Second),
	}
	connector.prefillsMu.Unlock()
	if _, ok := connector.loginPrefill(flowID, user); ok {
		t.Fatal("expected expired prefill to be pruned")
	}
}

func TestMapDiscoveredGatewaysPrefersResolvedEndpointAndTLS(t *testing.T) {
	results := mapDiscoveredGateways([]gatewayBonjourBeacon{
		{
			InstanceName: "Office",
			Domain:       "local.",
			DisplayName:  "Office",
			Host:         "gateway.local",
			Port:         443,
			LanHost:      "192.168.1.22",
			TailnetDNS:   "gateway.tailnet.ts.net",
			GatewayTLS:   true,
		},
	})
	if len(results) != 1 {
		t.Fatalf("unexpected discovery result count: %d", len(results))
	}
	if results[0].GatewayURL != "wss://gateway.local:443" {
		t.Fatalf("unexpected gateway url: %q", results[0].GatewayURL)
	}
	if results[0].Source != "mdns" {
		t.Fatalf("unexpected source: %q", results[0].Source)
	}
}

func TestProvisioningDiscoveryOptions(t *testing.T) {
	api := &openClawDiscoveryProvisioningAPI{
		connector: &OpenClawConnector{
			Config: Config{
				OpenClaw: OpenClawConfig{
					Discovery: OpenClawDiscoveryConfig{
						TimeoutMS:      2000,
						WideAreaDomain: "tail.example.com",
					},
				},
			},
		},
	}

	req := httptest.NewRequest("GET", "/v1/discovery/gateways?timeout_ms=1500&wide_area=on", nil)
	opts, err := api.discoveryOptions(req)
	if err != nil {
		t.Fatalf("discoveryOptions returned error: %v", err)
	}
	if opts.Timeout != 1500*time.Millisecond {
		t.Fatalf("unexpected timeout: %v", opts.Timeout)
	}
	if !opts.WideAreaEnabled || opts.WideAreaDomain != "tail.example.com" {
		t.Fatalf("unexpected wide-area options: %#v", opts)
	}

	req = httptest.NewRequest("GET", "/v1/discovery/gateways?timeout_ms=0", nil)
	if _, err := api.discoveryOptions(req); err == nil {
		t.Fatal("expected invalid timeout to fail")
	}

	api.connector.Config.OpenClaw.Discovery.WideAreaDomain = ""
	req = httptest.NewRequest("GET", "/v1/discovery/gateways?wide_area=on", nil)
	if _, err := api.discoveryOptions(req); err == nil {
		t.Fatal("expected wide_area=on without configured domain to fail")
	}
}
