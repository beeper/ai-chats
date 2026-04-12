package ai

import "testing"

func TestDesktopAPIInstancesMergesFallbackTokenIntoDefaultInstance(t *testing.T) {
	client := newTestAIClientWithProvider("")
	setTestLoginConfig(client, &aiLoginConfig{
		Credentials: &LoginCredentials{
			ServiceTokens: &ServiceTokens{
				DesktopAPI: "fallback-token",
				DesktopAPIInstances: map[string]DesktopAPIInstance{
					"default": {BaseURL: "https://desktop.example"},
				},
			},
		},
	})

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
