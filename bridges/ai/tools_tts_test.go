package ai

import (
	"testing"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func newTTSTestBridgeContext(meta *UserLoginMetadata, oc *OpenAIConnector) *BridgeToolContext {
	login := &database.UserLogin{
		ID:       networkid.UserLoginID("login"),
		Metadata: meta,
	}
	userLogin := &bridgev2.UserLogin{UserLogin: login, Log: zerolog.Nop()}
	client := &AIClient{
		UserLogin: userLogin,
		connector: oc,
	}
	return &BridgeToolContext{Client: client}
}

func TestResolveOpenAITTSBaseURLMagicProxy(t *testing.T) {
	meta := &UserLoginMetadata{
		Provider: ProviderMagicProxy,
		Credentials: &LoginCredentials{
			BaseURL: "https://bai.bt.hn/team/proxy",
		},
	}
	btc := newTTSTestBridgeContext(meta, &OpenAIConnector{})

	gotBaseURL, ok := resolveOpenAITTSBaseURL(btc, "https://bai.bt.hn/team/proxy/openrouter/v1")
	if !ok {
		t.Fatalf("expected magic proxy to support OpenAI TTS")
	}
	want := "https://bai.bt.hn/team/proxy/openai/v1"
	if gotBaseURL != want {
		t.Fatalf("unexpected magic proxy OpenAI TTS base URL: got %q want %q", gotBaseURL, want)
	}
}

func TestResolveOpenAITTSBaseURLMagicProxyWithoutConnector(t *testing.T) {
	meta := &UserLoginMetadata{
		Provider: ProviderMagicProxy,
		Credentials: &LoginCredentials{
			BaseURL: "https://bai.bt.hn/team/proxy/openrouter/v1",
		},
	}
	btc := newTTSTestBridgeContext(meta, nil)

	gotBaseURL, ok := resolveOpenAITTSBaseURL(btc, "https://bai.bt.hn/team/proxy/openrouter/v1")
	if !ok {
		t.Fatalf("expected magic proxy fallback resolution to support OpenAI TTS")
	}
	want := "https://bai.bt.hn/team/proxy/openai/v1"
	if gotBaseURL != want {
		t.Fatalf("unexpected magic proxy fallback OpenAI TTS base URL: got %q want %q", gotBaseURL, want)
	}
}

func TestResolveOpenAITTSBaseURLOpenAIProviderUsesConfiguredBase(t *testing.T) {
	meta := &UserLoginMetadata{Provider: ProviderOpenAI}
	oc := &OpenAIConnector{
		Config: Config{
			Models: &ModelsConfig{Providers: map[string]ModelProviderConfig{
				ProviderOpenAI: {BaseURL: "https://openai.example/v1"},
			}},
		},
	}
	btc := newTTSTestBridgeContext(meta, oc)

	gotBaseURL, ok := resolveOpenAITTSBaseURL(btc, "")
	if !ok {
		t.Fatalf("expected openai provider to support OpenAI TTS")
	}
	if gotBaseURL != "https://openai.example/v1" {
		t.Fatalf("unexpected configured OpenAI base URL: %q", gotBaseURL)
	}
}

func TestResolveOpenAITTSBaseURLOpenRouterNotSupported(t *testing.T) {
	meta := &UserLoginMetadata{Provider: ProviderOpenRouter}
	btc := newTTSTestBridgeContext(meta, &OpenAIConnector{})

	gotBaseURL, ok := resolveOpenAITTSBaseURL(btc, "https://openrouter.ai/api/v1")
	if ok {
		t.Fatalf("expected OpenRouter provider not to use OpenAI TTS path, got support with base %q", gotBaseURL)
	}
	if gotBaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("unexpected passthrough base URL: %q", gotBaseURL)
	}
}
