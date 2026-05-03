package connector

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2/networkid"
)

func TestValidateUserID(t *testing.T) {
	connector := &OpenAIConnector{}

	validModel := modelUserID("anthropic/claude-sonnet-4.6")
	invalidPrefix := networkid.UserID("user-someone")
	invalidEscapedModel := networkid.UserID("model-%ZZ")
	agentGhost := networkid.UserID("agent-beeper")
	unknownModel := modelUserID("openrouter/openai/not-a-real-model")

	if !connector.ValidateUserID(validModel) {
		t.Fatalf("expected valid model user ID %q", validModel)
	}
	if connector.ValidateUserID(invalidPrefix) {
		t.Fatalf("expected invalid prefix %q to be rejected", invalidPrefix)
	}
	if connector.ValidateUserID(invalidEscapedModel) {
		t.Fatalf("expected malformed model ID %q to be rejected", invalidEscapedModel)
	}
	if connector.ValidateUserID(agentGhost) {
		t.Fatalf("expected agent ghost ID %q to be rejected", agentGhost)
	}
	if connector.ValidateUserID(unknownModel) {
		t.Fatalf("expected unknown model ID %q to be rejected", unknownModel)
	}
}
