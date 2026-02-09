package connector

import "testing"

func TestResolveImageGenProviderMagicProxyPrefersOpenRouterForSimplePrompts(t *testing.T) {
	meta := &UserLoginMetadata{
		Provider: ProviderMagicProxy,
		APIKey:   "tok",
		BaseURL:  "https://bai.bt.hn/team/proxy",
	}
	btc := newTTSTestBridgeContext(meta, &OpenAIConnector{})

	got, err := resolveImageGenProvider(imageGenRequest{
		Prompt: "cat",
		Count:  1,
	}, btc)
	if err != nil {
		t.Fatalf("resolveImageGenProvider returned error: %v", err)
	}
	if got != imageGenProviderOpenRouter {
		t.Fatalf("expected provider %q, got %q", imageGenProviderOpenRouter, got)
	}
}

func TestResolveImageGenProviderMagicProxyStillPrefersOpenRouterWhenCountIsGreaterThanOne(t *testing.T) {
	meta := &UserLoginMetadata{
		Provider: ProviderMagicProxy,
		APIKey:   "tok",
		BaseURL:  "https://bai.bt.hn/team/proxy",
	}
	btc := newTTSTestBridgeContext(meta, &OpenAIConnector{})

	got, err := resolveImageGenProvider(imageGenRequest{
		Prompt: "cat",
		Count:  2,
	}, btc)
	if err != nil {
		t.Fatalf("resolveImageGenProvider returned error: %v", err)
	}
	if got != imageGenProviderOpenRouter {
		t.Fatalf("expected provider %q, got %q", imageGenProviderOpenRouter, got)
	}
}

func TestResolveImageGenProviderMagicProxyProviderOpenAIStillRoutesToOpenRouter(t *testing.T) {
	meta := &UserLoginMetadata{
		Provider: ProviderMagicProxy,
		APIKey:   "tok",
		BaseURL:  "https://bai.bt.hn/team/proxy",
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

func TestBuildOpenAIImagesBaseURLMagicProxy(t *testing.T) {
	meta := &UserLoginMetadata{
		Provider: ProviderMagicProxy,
		APIKey:   "tok",
		BaseURL:  "https://bai.bt.hn/team/proxy",
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
