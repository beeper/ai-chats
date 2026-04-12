package ai

import "testing"

func newTTSTestBridgeContext(provider string, cfg *aiLoginConfig, oc *OpenAIConnector) *BridgeToolContext {
	client := newTestAIClientWithProvider(provider)
	client.connector = oc
	setTestLoginConfig(client, cfg)
	return &BridgeToolContext{Client: client}
}

func TestResolveOpenAITTSBaseURLMagicProxy(t *testing.T) {
	btc := newTTSTestBridgeContext(ProviderMagicProxy, &aiLoginConfig{
		Credentials: &LoginCredentials{BaseURL: "https://bai.bt.hn/team/proxy"},
	}, &OpenAIConnector{})

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
	btc := newTTSTestBridgeContext(ProviderMagicProxy, &aiLoginConfig{
		Credentials: &LoginCredentials{BaseURL: "https://bai.bt.hn/team/proxy/openrouter/v1"},
	}, nil)

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
	oc := &OpenAIConnector{
		Config: Config{
			Models: &ModelsConfig{Providers: map[string]ModelProviderConfig{
				ProviderOpenAI: {BaseURL: "https://openai.example/v1"},
			}},
		},
	}
	btc := newTTSTestBridgeContext(ProviderOpenAI, nil, oc)

	gotBaseURL, ok := resolveOpenAITTSBaseURL(btc, "")
	if !ok {
		t.Fatalf("expected openai provider to support OpenAI TTS")
	}
	if gotBaseURL != "https://openai.example/v1" {
		t.Fatalf("unexpected configured OpenAI base URL: %q", gotBaseURL)
	}
}

func TestResolveOpenAITTSBaseURLOpenRouterNotSupported(t *testing.T) {
	btc := newTTSTestBridgeContext(ProviderOpenRouter, nil, &OpenAIConnector{})

	gotBaseURL, ok := resolveOpenAITTSBaseURL(btc, "https://openrouter.ai/api/v1")
	if ok {
		t.Fatalf("expected OpenRouter provider not to use OpenAI TTS path, got support with base %q", gotBaseURL)
	}
	if gotBaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("unexpected passthrough base URL: %q", gotBaseURL)
	}
}
