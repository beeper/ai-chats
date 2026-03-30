package ai

import (
	"testing"
)

func TestResolveImageGenProviderMagicProxyPrefersOpenAIForSimplePrompts(t *testing.T) {
	meta := &UserLoginMetadata{
		Provider: ProviderMagicProxy,
		Credentials: &LoginCredentials{
			APIKey:  "tok",
			BaseURL: "https://bai.bt.hn/team/proxy",
		},
	}
	btc := newTTSTestBridgeContext(meta, &OpenAIConnector{})

	got, err := resolveImageGenProvider(imageGenRequest{
		Prompt: "cat",
		Count:  1,
	}, btc)
	if err != nil {
		t.Fatalf("resolveImageGenProvider returned error: %v", err)
	}
	if got != imageGenProviderOpenAI {
		t.Fatalf("expected provider %q, got %q", imageGenProviderOpenAI, got)
	}
}

func TestResolveImageGenProviderMagicProxyStillPrefersOpenAIWhenCountIsGreaterThanOne(t *testing.T) {
	meta := &UserLoginMetadata{
		Provider: ProviderMagicProxy,
		Credentials: &LoginCredentials{
			APIKey:  "tok",
			BaseURL: "https://bai.bt.hn/team/proxy",
		},
	}
	btc := newTTSTestBridgeContext(meta, &OpenAIConnector{})

	got, err := resolveImageGenProvider(imageGenRequest{
		Prompt: "cat",
		Count:  2,
	}, btc)
	if err != nil {
		t.Fatalf("resolveImageGenProvider returned error: %v", err)
	}
	if got != imageGenProviderOpenAI {
		t.Fatalf("expected provider %q, got %q", imageGenProviderOpenAI, got)
	}
}

func TestResolveImageGenProviderMagicProxyProviderOpenAIUsesOpenAI(t *testing.T) {
	meta := &UserLoginMetadata{
		Provider: ProviderMagicProxy,
		Credentials: &LoginCredentials{
			APIKey:  "tok",
			BaseURL: "https://bai.bt.hn/team/proxy",
		},
	}
	btc := newTTSTestBridgeContext(meta, &OpenAIConnector{})

	got, err := resolveImageGenProvider(imageGenRequest{
		Provider: "openai",
		Prompt:   "cat",
		Count:    1,
	}, btc)
	if err != nil {
		t.Fatalf("resolveImageGenProvider returned error: %v", err)
	}
	if got != imageGenProviderOpenAI {
		t.Fatalf("expected provider %q, got %q", imageGenProviderOpenAI, got)
	}
}

func TestResolveImageGenProviderMagicProxyModelHintFallsBackToOpenAI(t *testing.T) {
	meta := &UserLoginMetadata{
		Provider: ProviderMagicProxy,
		Credentials: &LoginCredentials{
			APIKey:  "tok",
			BaseURL: "https://bai.bt.hn/team/proxy",
		},
	}
	btc := newTTSTestBridgeContext(meta, &OpenAIConnector{})

	got, err := resolveImageGenProvider(imageGenRequest{
		Model:  "google/gemini-3-pro-image-preview",
		Prompt: "cat",
		Count:  1,
	}, btc)
	if err != nil {
		t.Fatalf("resolveImageGenProvider returned error: %v", err)
	}
	if got != imageGenProviderOpenAI {
		t.Fatalf("expected provider %q, got %q", imageGenProviderOpenAI, got)
	}
}

func TestResolveImageGenProviderMagicProxyProviderGeminiIsUnavailable(t *testing.T) {
	meta := &UserLoginMetadata{
		Provider: ProviderMagicProxy,
		Credentials: &LoginCredentials{
			APIKey:  "tok",
			BaseURL: "https://bai.bt.hn/team/proxy",
		},
	}
	btc := newTTSTestBridgeContext(meta, &OpenAIConnector{})

	_, err := resolveImageGenProvider(imageGenRequest{
		Provider: "gemini",
		Prompt:   "cat",
		Count:    1,
	}, btc)
	if err == nil {
		t.Fatal("expected gemini image generation to be unavailable for magic proxy")
	}
}

func TestNormalizeOpenAIModelMapsUnavailableAliasesToGPTImage1(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: "gpt-image-1"},
		{name: "prefixed alias", input: "openai/gpt-5-image", want: "gpt-image-1"},
		{name: "mini alias", input: "gpt-5-image-mini", want: "gpt-image-1"},
		{name: "gemini alias", input: "google/gemini-3-pro-image-preview", want: "gpt-image-1"},
		{name: "native openai", input: "gpt-image-1", want: "gpt-image-1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeOpenAIModel(tc.input); got != tc.want {
				t.Fatalf("normalizeOpenAIModel(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestBuildOpenAIImagesBaseURLMagicProxy(t *testing.T) {
	meta := &UserLoginMetadata{
		Provider: ProviderMagicProxy,
		Credentials: &LoginCredentials{
			APIKey:  "tok",
			BaseURL: "https://bai.bt.hn/team/proxy",
		},
	}
	btc := newTTSTestBridgeContext(meta, &OpenAIConnector{})

	baseURL, err := buildOpenAIImagesBaseURL(btc)
	if err != nil {
		t.Fatalf("buildOpenAIImagesBaseURL returned error: %v", err)
	}
	if baseURL != "https://bai.bt.hn/team/proxy/openai/v1" {
		t.Fatalf("unexpected base url: %q", baseURL)
	}
}

func TestBuildGeminiBaseURLMagicProxy(t *testing.T) {
	meta := &UserLoginMetadata{
		Provider: ProviderMagicProxy,
		Credentials: &LoginCredentials{
			APIKey:  "tok",
			BaseURL: "https://bai.bt.hn/team/proxy",
		},
	}
	btc := newTTSTestBridgeContext(meta, &OpenAIConnector{})

	baseURL, err := buildGeminiBaseURL(btc)
	if err != nil {
		t.Fatalf("buildGeminiBaseURL returned error: %v", err)
	}
	if baseURL != "https://bai.bt.hn/team/proxy/gemini/v1beta" {
		t.Fatalf("unexpected base url: %q", baseURL)
	}
}

func TestResolveImageGenProviderRejectsMissingLoginMetadata(t *testing.T) {
	btc := &BridgeToolContext{
		Client: &AIClient{},
	}

	if _, err := resolveImageGenProvider(imageGenRequest{Prompt: "cat"}, btc); err == nil {
		t.Fatal("expected missing login metadata to be rejected")
	}
}
