package ai

import "testing"

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
