package connector

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"
)

func TestResolveManagedBeeperAuthUsesConfig(t *testing.T) {
	oc := &OpenAIConnector{
		Config: Config{
			Beeper: BeeperConfig{
				UserMXID: "@config:beeper.com",
				BaseURL:  "https://matrix.beeper.com",
				Token:    "config-token",
			},
		},
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

func TestResolveManagedBeeperAuthDoesNotUseRuntimeFallback(t *testing.T) {
	oc := &OpenAIConnector{
		Config: Config{
			Beeper: BeeperConfig{
				UserMXID: "@config:beeper.com",
			},
		},
	}

	auth := oc.resolveManagedBeeperAuth()
	if auth.UserMXID != id.UserID("@config:beeper.com") {
		t.Fatalf("expected config mxid, got %q", auth.UserMXID)
	}
	if auth.BaseURL != "" {
		t.Fatalf("expected empty base url, got %q", auth.BaseURL)
	}
	if auth.Token != "" {
		t.Fatalf("expected empty token, got %q", auth.Token)
	}
	if auth.Complete() {
		t.Fatal("expected auth tuple to be incomplete")
	}
}

func TestManagedBeeperLoginID(t *testing.T) {
	got := managedBeeperLoginID(id.UserID("@user:beeper.com"))
	want := "managed-beeper:@user:beeper.com"
	if string(got) != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestSelectPreferredUserLoginFallsBackFromBrokenManaged(t *testing.T) {
	managed := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: managedBeeperLoginID(id.UserID("@user:beeper.com"))}}
	manual := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: providerLoginID(ProviderOpenAI, id.UserID("@user:beeper.com"), 1)}}

	selected := selectPreferredUserLogin(
		managed,
		managed,
		[]*bridgev2.UserLogin{managed, manual},
		func(login *bridgev2.UserLogin) bool { return login == manual },
	)
	if selected != manual {
		t.Fatalf("expected manual login fallback, got %#v", selected)
	}
}
