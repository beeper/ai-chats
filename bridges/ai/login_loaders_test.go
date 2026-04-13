package ai

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote/sdk"
)

func testUserLoginWithMeta(loginID networkid.UserLoginID, meta *UserLoginMetadata) *bridgev2.UserLogin {
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			ID: loginID,
		},
	}
	if meta != nil {
		login.UserLogin.Metadata = meta
	}
	return login
}

func TestAIClientNeedsRebuild(t *testing.T) {
	existing := &AIClient{
		apiKey:      "secret",
		UserLogin:   testUserLoginWithMeta("existing", &UserLoginMetadata{Provider: " OpenAI "}),
		loginConfig: &aiLoginConfig{Credentials: &LoginCredentials{BaseURL: "https://api.example.com/v1/"}},
	}

	if aiClientNeedsRebuildConfig(existing, "secret", "openai", &aiLoginConfig{Credentials: &LoginCredentials{BaseURL: "https://api.example.com/v1"}}) {
		t.Fatal("expected no rebuild when key/provider/base URL are equivalent")
	}
	if !aiClientNeedsRebuildConfig(existing, "other-key", "openai", &aiLoginConfig{Credentials: &LoginCredentials{BaseURL: "https://api.example.com/v1"}}) {
		t.Fatal("expected rebuild when API key changes")
	}
	if !aiClientNeedsRebuildConfig(existing, "secret", "openrouter", &aiLoginConfig{Credentials: &LoginCredentials{BaseURL: "https://api.example.com/v1"}}) {
		t.Fatal("expected rebuild when provider changes")
	}
	if !aiClientNeedsRebuildConfig(existing, "secret", "openai", &aiLoginConfig{Credentials: &LoginCredentials{BaseURL: "https://api.other.example.com/v1"}}) {
		t.Fatal("expected rebuild when base URL changes")
	}
	if !aiClientNeedsRebuildConfig(nil, "secret", "openai", &aiLoginConfig{}) {
		t.Fatal("expected rebuild when no existing client is cached")
	}
}

func TestLoadAIUserLoginMissingAPIKeyEvictsCacheAndSetsBrokenClient(t *testing.T) {
	loginID := networkid.UserLoginID("login-1")
	oc := &OpenAIConnector{
		clients: map[networkid.UserLoginID]bridgev2.NetworkAPI{},
	}
	cachedLogin := testUserLoginWithMeta(loginID, nil)
	oc.clients[loginID] = newBrokenLoginClient(cachedLogin, "cached")

	login := testUserLoginWithMeta(loginID, nil)
	if err := oc.loadAIUserLogin(context.Background(), login, &UserLoginMetadata{Provider: ProviderOpenAI}); err != nil {
		t.Fatalf("loadAIUserLogin returned error: %v", err)
	}
	if _, ok := oc.clients[loginID]; ok {
		t.Fatal("expected cached client to be evicted when API key is missing")
	}
	if login.Client == nil {
		t.Fatal("expected broken login client")
	}
	if _, ok := login.Client.(*sdk.BrokenLoginClient); !ok {
		t.Fatalf("expected broken login client type, got %T", login.Client)
	}
}

func TestLoadAIUserLoginMagicProxyBuildsClientFromPersistedConfig(t *testing.T) {
	client := newDBBackedTestAIClient(t, ProviderMagicProxy)
	login := client.UserLogin
	loginID := login.ID
	if login.Bridge != nil {
		login.Bridge.BackgroundCtx = context.Background()
	}
	agentsDisabled := false

	if err := saveAILoginConfig(context.Background(), login, &aiLoginConfig{
		Agents: &agentsDisabled,
		Credentials: &LoginCredentials{
			APIKey:  "proxy-token",
			BaseURL: "https://temporary-ai-proxy.beeper-tools.com",
		},
	}); err != nil {
		t.Fatalf("saveAILoginConfig returned error: %v", err)
	}

	login.Client = newBrokenLoginClient(login, "broken")
	oc := &OpenAIConnector{
		clients: map[networkid.UserLoginID]bridgev2.NetworkAPI{},
	}

	if err := oc.loadAIUserLogin(context.Background(), login, &UserLoginMetadata{Provider: ProviderMagicProxy}); err != nil {
		t.Fatalf("loadAIUserLogin returned error: %v", err)
	}

	typed, ok := login.Client.(*AIClient)
	if !ok {
		t.Fatalf("expected AIClient after loading persisted magic proxy config, got %T", login.Client)
	}
	if typed.apiKey != "proxy-token" {
		t.Fatalf("unexpected api key on loaded client: %q", typed.apiKey)
	}
	if typed.provider == nil {
		t.Fatal("expected initialized provider for magic proxy login")
	}
	if _, ok := oc.clients[loginID].(*AIClient); !ok {
		t.Fatalf("expected cached AI client for %q", loginID)
	}
}

func TestReuseAIClientUpdatesClientBaseLogin(t *testing.T) {
	login := testUserLoginWithMeta("login-2", &UserLoginMetadata{Provider: ProviderOpenAI})
	client := &AIClient{}

	reuseAIClient(login, client, false)

	if client.UserLogin != login {
		t.Fatal("expected user login to be updated on the client")
	}
	if client.GetUserLogin() != login {
		t.Fatal("expected embedded ClientBase login to be updated")
	}
	if login.Client != client {
		t.Fatal("expected login client reference to point at the reused client")
	}
}
