package ai

import (
	"testing"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
)

func TestInitProviderForLoginRejectsNilMetadata(t *testing.T) {
	provider, err := initProviderForLogin("test-key", nil, &OpenAIConnector{}, &bridgev2.UserLogin{}, zerolog.Nop())
	if err == nil {
		t.Fatal("expected nil metadata to be rejected")
	}
	if provider != nil {
		t.Fatalf("expected no provider on nil metadata, got %#v", provider)
	}
}
