package connector

import "testing"

func TestImplicitModelCatalogEntries_MagicProxySeedsCatalog(t *testing.T) {
	oc := &AIClient{
		connector: &OpenAIConnector{},
	}

	// Magic Proxy logins store the API key on the login metadata.
	meta := &UserLoginMetadata{
		Provider: ProviderMagicProxy,
		APIKey:   "mp-token",
	}

	entries := oc.implicitModelCatalogEntries(meta)
	if len(entries) == 0 {
		t.Fatalf("expected non-empty model catalog entries for magic_proxy, got 0")
	}
}

