package connector

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2/networkid"
)

func TestAgentUserIDForLoginScopesCustomAgents(t *testing.T) {
	loginID := networkid.UserLoginID("openai:@user:example.com")
	custom := agentUserIDForLogin(loginID, "my-agent")
	if custom == agentUserID("my-agent") {
		t.Fatalf("expected custom agent ghost ID to be login-scoped, got %q", custom)
	}
	parsed, ok := parseAgentFromGhostID(string(custom))
	if !ok || parsed != "my-agent" {
		t.Fatalf("expected scoped ghost ID to parse back to agent ID, got %q ok=%v", parsed, ok)
	}
}

func TestAgentUserIDForLoginKeepsBuiltinAgentsGlobal(t *testing.T) {
	loginID := networkid.UserLoginID("openai:@user:example.com")
	got := agentUserIDForLogin(loginID, "beeper")
	if got != agentUserID("beeper") {
		t.Fatalf("expected built-in agent ghost ID to stay global, got %q", got)
	}
}
