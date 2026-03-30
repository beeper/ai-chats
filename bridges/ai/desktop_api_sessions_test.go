package ai

import (
	"testing"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func TestDesktopAPIInstancesMergesFallbackTokenIntoDefaultInstance(t *testing.T) {
	client := &AIClient{
		UserLogin: &bridgev2.UserLogin{
			UserLogin: &database.UserLogin{
				ID: networkid.UserLoginID("login"),
				Metadata: &UserLoginMetadata{
					Credentials: &LoginCredentials{
						ServiceTokens: &ServiceTokens{
							DesktopAPI: "fallback-token",
							DesktopAPIInstances: map[string]DesktopAPIInstance{
								"default": {BaseURL: "https://desktop.example"},
							},
						},
					},
				},
			},
			Log: zerolog.Nop(),
		},
	}

	instances := client.desktopAPIInstances()
	got, ok := instances[desktopDefaultInstance]
	if !ok {
		t.Fatal("expected default desktop API instance")
	}
	if got.Token != "fallback-token" {
		t.Fatalf("expected fallback token to be merged, got %#v", got)
	}
	if got.BaseURL != "https://desktop.example" {
		t.Fatalf("expected base URL to be preserved, got %#v", got)
	}
}
