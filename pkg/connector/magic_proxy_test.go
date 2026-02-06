package connector

import (
	"strings"
	"testing"
)

func TestNormalizeMagicProxyBaseURLPreservesPath(t *testing.T) {
	got := normalizeMagicProxyBaseURL("bai.bt.hn/team/proxy/?foo=bar#token")
	want := "https://bai.bt.hn/team/proxy"
	if got != want {
		t.Fatalf("unexpected normalized URL: got %q want %q", got, want)
	}
}

func TestNormalizeMagicProxyBaseURLStripsServicePath(t *testing.T) {
	got := normalizeMagicProxyBaseURL("https://bai.bt.hn/team/proxy/openrouter/v1#token")
	want := "https://bai.bt.hn/team/proxy"
	if got != want {
		t.Fatalf("unexpected normalized URL: got %q want %q", got, want)
	}
}

func TestParseMagicProxyLinkPreservesPath(t *testing.T) {
	baseURL, token, err := parseMagicProxyLink("https://bai.bt.hn/team/proxy?foo=bar#abc123")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if token != "abc123" {
		t.Fatalf("unexpected token: got %q", token)
	}
	wantBase := "https://bai.bt.hn/team/proxy"
	if baseURL != wantBase {
		t.Fatalf("unexpected base URL: got %q want %q", baseURL, wantBase)
	}
}

func TestResolveServiceConfigMagicProxyUsesJoinedPaths(t *testing.T) {
	oc := &OpenAIConnector{}
	meta := &UserLoginMetadata{
		Provider: ProviderMagicProxy,
		APIKey:   "tok",
		BaseURL:  "https://bai.bt.hn/team/proxy",
	}

	services := oc.resolveServiceConfig(meta)

	if got := services[serviceOpenRouter].BaseURL; got != "https://bai.bt.hn/team/proxy/openrouter/v1" {
		t.Fatalf("unexpected openrouter base URL: %q", got)
	}
	if got := services[serviceOpenAI].BaseURL; got != "https://bai.bt.hn/team/proxy/openai/v1" {
		t.Fatalf("unexpected openai base URL: %q", got)
	}
	if got := services[serviceExa].BaseURL; got != "https://bai.bt.hn/team/proxy/exa" {
		t.Fatalf("unexpected exa base URL: %q", got)
	}
}

func TestResolveServiceConfigMagicProxyNoDuplicateOpenRouterPath(t *testing.T) {
	oc := &OpenAIConnector{}
	meta := &UserLoginMetadata{
		Provider: ProviderMagicProxy,
		APIKey:   "tok",
		BaseURL:  "https://bai.bt.hn/team/proxy/openrouter/v1",
	}

	services := oc.resolveServiceConfig(meta)
	base := services[serviceOpenRouter].BaseURL
	if strings.Count(base, "/openrouter/v1") != 1 {
		t.Fatalf("openrouter path duplicated: %q", base)
	}
	if got := services[serviceExa].BaseURL; got != "https://bai.bt.hn/team/proxy/exa" {
		t.Fatalf("unexpected exa base URL: %q", got)
	}
}

func TestResolveExaProxyBaseURLMagicProxyPrefersLoginBase(t *testing.T) {
	oc := &OpenAIConnector{
		Config: Config{
			Beeper: BeeperConfig{
				BaseURL: "https://matrix.example.com",
			},
		},
	}
	meta := &UserLoginMetadata{
		Provider: ProviderMagicProxy,
		BaseURL:  "https://ai.bt.hn/",
	}
	if got := oc.resolveExaProxyBaseURL(meta); got != "https://ai.bt.hn/exa" {
		t.Fatalf("unexpected exa proxy base: %q", got)
	}
}
