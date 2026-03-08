package connector

import (
	"testing"

	"maunium.net/go/mautrix/id"
)

func TestResolveManagedBeeperAuthConfigOverridesRuntime(t *testing.T) {
	oc := &OpenAIConnector{
		Config: Config{
			Beeper: BeeperConfig{
				UserMXID: "@config:beeper.com",
				BaseURL:  "https://matrix.beeper.com",
				Token:    "config-token",
			},
		},
		localAIBridgeLoginUserMXID:   "@runtime:beeper.com",
		localAIBridgeLoginToken:      "runtime-token",
		localAIBridgeLoginHomeserver: "https://matrix.runtime.com",
	}

	auth := oc.resolveManagedBeeperAuth()
	if auth.UserMXID != id.UserID("@config:beeper.com") {
		t.Fatalf("expected config mxid, got %q", auth.UserMXID)
	}
	if auth.BaseURL != "https://matrix.beeper.com/_matrix/client/unstable/com.beeper.ai" {
		t.Fatalf("unexpected base url: %q", auth.BaseURL)
	}
	if auth.Token != "config-token" {
		t.Fatalf("expected config token, got %q", auth.Token)
	}
}

func TestResolveManagedBeeperAuthUsesRuntimeForMissingConfigFields(t *testing.T) {
	oc := &OpenAIConnector{
		Config: Config{
			Beeper: BeeperConfig{
				UserMXID: "@config:beeper.com",
			},
		},
		localAIBridgeLoginToken:      "runtime-token",
		localAIBridgeLoginHomeserver: "matrix.runtime.com",
	}

	auth := oc.resolveManagedBeeperAuth()
	if auth.UserMXID != id.UserID("@config:beeper.com") {
		t.Fatalf("expected config mxid, got %q", auth.UserMXID)
	}
	if auth.BaseURL != "https://matrix.runtime.com/_matrix/client/unstable/com.beeper.ai" {
		t.Fatalf("unexpected runtime base url: %q", auth.BaseURL)
	}
	if auth.Token != "runtime-token" {
		t.Fatalf("expected runtime token, got %q", auth.Token)
	}
	if !auth.Complete() {
		t.Fatal("expected auth tuple to be complete")
	}
}

func TestGetLoginFlowsHidesManagedBeeperFlowWhenAuthAvailable(t *testing.T) {
	oc := &OpenAIConnector{}
	oc.SetLocalAIBridgeLogin(id.UserID("@user:beeper.com"), "runtime-token", "https://matrix.beeper.com")

	flows := oc.GetLoginFlows()
	for _, flow := range flows {
		if flow.ID == ProviderBeeper {
			t.Fatalf("expected Beeper Cloud flow to be hidden, got %+v", flow)
		}
	}
}

func TestManagedBeeperLoginID(t *testing.T) {
	got := managedBeeperLoginID(id.UserID("@user:beeper.com"))
	want := "beeper:@user:beeper.com"
	if string(got) != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
