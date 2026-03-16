package ai

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

func TestFindModelInfoWithNilLoginMetadataDoesNotPanic(t *testing.T) {
	client := &AIClient{
		UserLogin: &bridgev2.UserLogin{
			UserLogin: &database.UserLogin{},
		},
	}

	if got := client.findModelInfo("missing-model"); got != nil {
		t.Fatalf("expected nil model info for unknown model id, got %#v", got)
	}
}
