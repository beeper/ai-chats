package providerchain

import (
	"errors"
	"testing"
)

type testProvider struct {
	name string
}

func TestRunFirstFinalizesAndReturnsFirstSuccess(t *testing.T) {
	providers := map[string]testProvider{
		"first":  {name: "first"},
		"second": {name: "second"},
	}

	var finalized string
	resp, err := RunFirst(
		[]string{"missing", "first", "second"},
		func(name string) (testProvider, bool) {
			provider, ok := providers[name]
			return provider, ok
		},
		func(provider testProvider) (*string, error) {
			value := provider.name
			return &value, nil
		},
		func(name string, resp *string) {
			finalized = name + ":" + *resp
		},
		errors.New("unavailable"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || *resp != "first" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if finalized != "first:first" {
		t.Fatalf("unexpected finalize value %q", finalized)
	}
}

func TestRunFirstReturnsLastProviderError(t *testing.T) {
	want := errors.New("boom")
	_, err := RunFirst(
		[]string{"first", "second"},
		func(name string) (testProvider, bool) {
			return testProvider{name: name}, true
		},
		func(provider testProvider) (*string, error) {
			if provider.name == "second" {
				return nil, want
			}
			return nil, errors.New("skip")
		},
		nil,
		errors.New("unavailable"),
	)
	if !errors.Is(err, want) {
		t.Fatalf("expected last error %v, got %v", want, err)
	}
}
