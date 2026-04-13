package ai

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func TestShouldEnsureDefaultChat(t *testing.T) {
	enabled := true
	disabled := false

	tests := []struct {
		name string
		cfg  *aiLoginConfig
		want bool
	}{
		{
			name: "nil config",
			cfg:  nil,
			want: false,
		},
		{
			name: "new login with nil agents defaults disabled",
			cfg:  &aiLoginConfig{},
			want: false,
		},
		{
			name: "agents enabled",
			cfg:  &aiLoginConfig{Agents: &enabled},
			want: true,
		},
		{
			name: "agents disabled",
			cfg:  &aiLoginConfig{Agents: &disabled},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldEnsureDefaultChat(tc.cfg); got != tc.want {
				t.Fatalf("shouldEnsureDefaultChat() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAgentsEnabledForLogin_DefaultsDisabledAndConfigControlsEnablement(t *testing.T) {
	enabled := true
	disabled := false

	client := newDBBackedTestAIClient(t, ProviderMagicProxy)
	if client.agentsEnabledForLogin() {
		t.Fatalf("expected agents to be disabled by default")
	}

	setTestLoginConfig(client, &aiLoginConfig{Agents: &enabled})
	if !client.agentsEnabledForLogin() {
		t.Fatalf("expected config to enable agents")
	}

	setTestLoginConfig(client, &aiLoginConfig{Agents: &disabled})
	if client.agentsEnabledForLogin() {
		t.Fatalf("expected config to disable agents")
	}
}

func TestEnsureDefaultChatReusesExistingVisibleChat(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderMagicProxy)

	existingKey := networkid.PortalKey{
		ID:       networkid.PortalID("existing-chat"),
		Receiver: client.UserLogin.ID,
	}
	existingPortal := &bridgev2.Portal{
		Portal: &database.Portal{
			BridgeID:  client.UserLogin.Bridge.ID,
			PortalKey: existingKey,
			MXID:      id.RoomID("!existing:example.com"),
			Metadata:  &PortalMetadata{Slug: "chat-2"},
		},
		Bridge: client.UserLogin.Bridge,
	}
	setUnexportedField(client.UserLogin.Bridge, "portalsByKey", map[networkid.PortalKey]*bridgev2.Portal{
		existingKey: existingPortal,
	})
	if err := saveAIPortalState(ctx, existingPortal, portalMeta(existingPortal)); err != nil {
		t.Fatalf("saveAIPortalState returned error: %v", err)
	}

	if err := client.ensureDefaultChat(ctx); err != nil {
		t.Fatalf("ensureDefaultChat returned error: %v", err)
	}
	defaultPortal, err := client.UserLogin.Bridge.GetExistingPortalByKey(ctx, defaultChatPortalKey(client.UserLogin.ID))
	if err != nil {
		t.Fatalf("GetExistingPortalByKey returned error: %v", err)
	}
	if defaultPortal != nil {
		t.Fatalf("expected existing visible chat to be reused instead of creating a new default portal")
	}
}
