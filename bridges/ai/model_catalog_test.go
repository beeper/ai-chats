package ai

import "testing"

func TestImplicitModelCatalogEntries_MagicProxySeedsCatalog(t *testing.T) {
	oc := &AIClient{
		connector: &OpenAIConnector{},
	}

	entries := oc.implicitModelCatalogEntries(ProviderMagicProxy, &aiLoginConfig{
		Credentials: &LoginCredentials{APIKey: "mp-token"},
	})
	if len(entries) == 0 {
		t.Fatalf("expected non-empty model catalog entries for magic_proxy, got 0")
	}
}

func TestImplicitModelCatalogEntries_OpenAILoginUsesManifestMetadata(t *testing.T) {
	oc := &AIClient{
		connector: &OpenAIConnector{},
	}
	entries := oc.implicitModelCatalogEntries(ProviderOpenAI, &aiLoginConfig{
		Credentials: &LoginCredentials{APIKey: "openai-token"},
	})
	if len(entries) == 0 {
		t.Fatalf("expected non-empty model catalog entries for openai, got 0")
	}

	entry := findModelCatalogEntry(entries, ProviderOpenAI, "gpt-5.4-mini")
	if entry == nil {
		t.Fatal("expected gpt-5.4-mini entry in openai catalog")
	}

	manifestInfo, ok := ModelManifest.Models["openai/gpt-5.4-mini"]
	if !ok {
		t.Fatal("expected gpt-5.4-mini in manifest")
	}

	if entry.ContextWindow != manifestInfo.ContextWindow {
		t.Fatalf("context window = %d, want %d", entry.ContextWindow, manifestInfo.ContextWindow)
	}
	if entry.MaxOutputTokens != manifestInfo.MaxOutputTokens {
		t.Fatalf("max output tokens = %d, want %d", entry.MaxOutputTokens, manifestInfo.MaxOutputTokens)
	}
	if !catalogInputIncludes(entry, "image") || !catalogInputIncludes(entry, "pdf") {
		t.Fatalf("expected openai catalog entry to retain manifest modalities, got %#v", entry.Input)
	}
}
