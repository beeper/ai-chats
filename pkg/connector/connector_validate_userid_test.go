package connector

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2/networkid"
)

func TestValidateUserID(t *testing.T) {
	connector := &OpenAIConnector{}

	validModel := modelUserID("anthropic/claude-sonnet-4.6")
	validAgent := agentUserID("beeper")
	validScopedAgent := agentUserIDForLogin(networkid.UserLoginID("openai:@user:example.com"), "my-agent")
	invalidPrefix := networkid.UserID("user-someone")
	invalidEscapedModel := networkid.UserID("model-%ZZ")
	invalidEscapedAgent := networkid.UserID("agent-%ZZ")
	unknownModel := modelUserID("openrouter/openai/not-a-real-model")

	if !connector.ValidateUserID(validModel) {
		t.Fatalf("expected valid model user ID %q", validModel)
	}
	if !connector.ValidateUserID(validAgent) {
		t.Fatalf("expected valid agent user ID %q", validAgent)
	}
	if !connector.ValidateUserID(validScopedAgent) {
		t.Fatalf("expected valid scoped agent user ID %q", validScopedAgent)
	}
	if connector.ValidateUserID(invalidPrefix) {
		t.Fatalf("expected invalid prefix %q to be rejected", invalidPrefix)
	}
	if connector.ValidateUserID(invalidEscapedModel) {
		t.Fatalf("expected malformed model ID %q to be rejected", invalidEscapedModel)
	}
	if connector.ValidateUserID(invalidEscapedAgent) {
		t.Fatalf("expected malformed agent ID %q to be rejected", invalidEscapedAgent)
	}
	if connector.ValidateUserID(unknownModel) {
		t.Fatalf("expected unknown model ID %q to be rejected", unknownModel)
	}
}
