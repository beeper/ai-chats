package oauth

import (
	"errors"
	"testing"
	"time"
)

type testProvider struct {
	id           ProviderID
	name         string
	apiKey       string
	refreshed    Credentials
	refreshErr   error
	usesCallback bool
}

func (p *testProvider) ID() ProviderID { return p.id }
func (p *testProvider) Name() string   { return p.name }
func (p *testProvider) Login(callbacks LoginCallbacks) (Credentials, error) {
	return Credentials{}, nil
}
func (p *testProvider) UsesCallbackServer() bool { return p.usesCallback }
func (p *testProvider) RefreshToken(credentials Credentials) (Credentials, error) {
	if p.refreshErr != nil {
		return Credentials{}, p.refreshErr
	}
	return p.refreshed, nil
}
func (p *testProvider) GetAPIKey(credentials Credentials) string {
	return p.apiKey
}

func TestProviderRegistryAndGetAPIKey(t *testing.T) {
	ResetProviders()
	p := &testProvider{
		id:     "test",
		name:   "Test",
		apiKey: "key-1",
		refreshed: Credentials{
			Refresh: "r2",
			Access:  "a2",
			Expires: time.Now().Add(time.Hour).UnixMilli(),
		},
	}
	RegisterProvider(p)

	got, ok := GetProvider("test")
	if !ok || got.Name() != "Test" {
		t.Fatalf("expected provider in registry")
	}

	credsMap := map[ProviderID]Credentials{
		"test": {
			Refresh: "r1",
			Access:  "a1",
			Expires: time.Now().Add(-time.Minute).UnixMilli(),
		},
	}
	newCreds, apiKey, err := GetAPIKey("test", credsMap)
	if err != nil {
		t.Fatalf("unexpected get api key error: %v", err)
	}
	if newCreds == nil || newCreds.Access != "a2" {
		t.Fatalf("expected refreshed credentials, got %#v", newCreds)
	}
	if apiKey != "key-1" {
		t.Fatalf("expected api key key-1, got %s", apiKey)
	}

	UnregisterProvider("test")
	if _, ok := GetProvider("test"); ok {
		t.Fatalf("expected provider removed")
	}
}

func TestGetAPIKey_RefreshFailure(t *testing.T) {
	ResetProviders()
	p := &testProvider{
		id:         "broken",
		name:       "Broken",
		apiKey:     "x",
		refreshErr: errors.New("boom"),
	}
	RegisterProvider(p)

	_, _, err := GetAPIKey("broken", map[ProviderID]Credentials{
		"broken": {
			Refresh: "r1",
			Access:  "a1",
			Expires: time.Now().Add(-time.Minute).UnixMilli(),
		},
	})
	if err == nil {
		t.Fatalf("expected refresh failure error")
	}
}
