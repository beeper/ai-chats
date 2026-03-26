package ai

import (
	"testing"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func newMediaTestClient(meta *UserLoginMetadata, oc *OpenAIConnector) *AIClient {
	login := &database.UserLogin{
		ID:       networkid.UserLoginID("login"),
		Metadata: meta,
	}
	userLogin := &bridgev2.UserLogin{UserLogin: login, Log: zerolog.Nop()}
	return &AIClient{
		UserLogin: userLogin,
		connector: oc,
	}
}

func TestResolveMediaProviderAPIKeyOpenAIMagicProxyUsesLoginToken(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	meta := &UserLoginMetadata{
		Provider: ProviderMagicProxy,
		APIKey:   "tok",
		BaseURL:  "https://bai.bt.hn/team/proxy",
	}
	client := newMediaTestClient(meta, &OpenAIConnector{})

	if got := client.resolveMediaProviderAPIKey("openai", "", ""); got != "tok" {
		t.Fatalf("unexpected key: %q", got)
	}
}

func TestResolveOpenAIMediaBaseURLMagicProxyUsesOpenAIServicePath(t *testing.T) {
	meta := &UserLoginMetadata{
		Provider: ProviderMagicProxy,
		APIKey:   "tok",
		BaseURL:  "https://bai.bt.hn/team/proxy",
	}
	client := newMediaTestClient(meta, &OpenAIConnector{})

	if got := resolveOpenAIMediaBaseURL(client); got != "https://bai.bt.hn/team/proxy/openai/v1" {
		t.Fatalf("unexpected base url: %q", got)
	}
}

func TestResolveOpenRouterMediaConfigUsesEntryOverrides(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY_SPECIAL_PROFILE", "entry-key")

	client := newMediaTestClient(&UserLoginMetadata{Provider: ProviderOpenAI}, &OpenAIConnector{
		Config: Config{
			Providers: ProvidersConfig{
				OpenRouter: ProviderConfig{
					DefaultPDFEngine: "native",
				},
			},
		},
	})

	cfg := &MediaUnderstandingConfig{
		BaseURL: "https://cfg.example/v1",
		Headers: map[string]string{
			"X-Config": "cfg",
		},
	}
	entry := MediaUnderstandingModelConfig{
		BaseURL: "https://entry.example/v1",
		Headers: map[string]string{
			"HTTP-Referer": "https://override.example",
			"X-Entry":      "entry",
		},
		Profile: "special-profile",
	}

	apiKey, baseURL, headers, pdfEngine, _, err := client.resolveOpenRouterMediaConfig(cfg, entry)
	if err != nil {
		t.Fatalf("resolveOpenRouterMediaConfig returned error: %v", err)
	}
	if apiKey != "entry-key" {
		t.Fatalf("expected entry-scoped API key, got %q", apiKey)
	}
	if baseURL != "https://entry.example/v1" {
		t.Fatalf("expected entry base url, got %q", baseURL)
	}
	if headers["X-Config"] != "cfg" {
		t.Fatalf("expected config header to be preserved, got %#v", headers)
	}
	if headers["X-Entry"] != "entry" {
		t.Fatalf("expected entry header to be preserved, got %#v", headers)
	}
	if headers["HTTP-Referer"] != "https://override.example" {
		t.Fatalf("expected entry referer override, got %#v", headers)
	}
	if headers["X-Title"] != openRouterAppTitle {
		t.Fatalf("expected default OpenRouter title header, got %#v", headers)
	}
	if pdfEngine != "native" {
		t.Fatalf("expected configured PDF engine, got %q", pdfEngine)
	}
}

func TestResolveOpenRouterMediaConfigAllowsAuthHeaderWithoutAPIKey(t *testing.T) {
	client := newMediaTestClient(&UserLoginMetadata{Provider: ProviderOpenAI}, &OpenAIConnector{})

	_, _, headers, _, _, err := client.resolveOpenRouterMediaConfig(nil, MediaUnderstandingModelConfig{
		Headers: map[string]string{
			"Authorization": "Bearer token",
		},
	})
	if err != nil {
		t.Fatalf("resolveOpenRouterMediaConfig returned error: %v", err)
	}
	if headers["Authorization"] != "Bearer token" {
		t.Fatalf("expected auth header to be preserved, got %#v", headers)
	}
}
