package ai

import (
	"context"
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/provisionutil"
	"maunium.net/go/mautrix/event"
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
	existingPortal, err := client.UserLogin.Bridge.GetPortalByKey(ctx, existingKey)
	if err != nil {
		t.Fatalf("GetPortalByKey returned error: %v", err)
	}
	existingPortal.MXID = id.RoomID("!existing:example.com")
	existingPortal.Metadata = &PortalMetadata{Slug: "chat-2"}
	if err := existingPortal.Save(ctx); err != nil {
		t.Fatalf("Portal.Save returned error: %v", err)
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

func TestBootstrapPortalRoomSendsInitialWelcomeNotice(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderMagicProxy)

	matrix := client.UserLogin.Bridge.Matrix.(*testMatrixConnector)
	ghostAPI := &testMatrixAPI{}
	botAPI := &testMatrixAPI{createRoomID: id.RoomID("!new-ai-chat:example.com")}
	matrix.api = ghostAPI
	client.UserLogin.Bridge.Bot = botAPI

	chatResp, err := client.createChat(ctx, chatCreateParams{ModelID: client.effectiveModel(nil)})
	if err != nil {
		t.Fatalf("createChat returned error: %v", err)
	}

	portal, err := client.ensurePortalRoom(ctx, ensurePortalRoomParams{
		Portal:   chatResp.Portal,
		ChatInfo: chatResp.PortalInfo,
	})
	if err != nil {
		t.Fatalf("ensurePortalRoom returned error: %v", err)
	}
	if portal.MXID == "" {
		t.Fatal("expected ensurePortalRoom to materialize a Matrix room")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ghostAPI.sentContent != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ghostAPI.sentContent == nil {
		t.Fatal("expected initial welcome notice to be sent")
	}
	if botAPI.sendCount != 0 {
		t.Fatalf("expected welcome notice to avoid bridge bot send path, got %d bot sends", botAPI.sendCount)
	}
	if ghostAPI.sentRoomID != portal.MXID {
		t.Fatalf("expected welcome notice in %q, got %q", portal.MXID, ghostAPI.sentRoomID)
	}
	if ghostAPI.sentType != event.EventMessage {
		t.Fatalf("expected event type %q, got %q", event.EventMessage, ghostAPI.sentType)
	}
	msg, ok := ghostAPI.sentContent.Parsed.(*event.MessageEventContent)
	if !ok {
		t.Fatalf("expected parsed message content, got %#v", ghostAPI.sentContent.Parsed)
	}
	if msg.MsgType != event.MsgNotice {
		t.Fatalf("expected notice message, got %q", msg.MsgType)
	}
	if !strings.Contains(msg.Body, "AI can make mistakes.") {
		t.Fatalf("expected AI disclaimer, got %q", msg.Body)
	}
	if meta := portalMeta(portal); meta == nil || !meta.WelcomeSent {
		t.Fatalf("expected WelcomeSent to be persisted, got %#v", meta)
	}
}

func TestEnsurePortalRoomDoesNotResendInitialWelcomeNotice(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderMagicProxy)

	matrix := client.UserLogin.Bridge.Matrix.(*testMatrixConnector)
	ghostAPI := &testMatrixAPI{}
	botAPI := &testMatrixAPI{createRoomID: id.RoomID("!new-ai-chat:example.com")}
	matrix.api = ghostAPI
	client.UserLogin.Bridge.Bot = botAPI

	chatResp, err := client.createChat(ctx, chatCreateParams{ModelID: client.effectiveModel(nil)})
	if err != nil {
		t.Fatalf("createChat returned error: %v", err)
	}

	portal, err := client.ensurePortalRoom(ctx, ensurePortalRoomParams{
		Portal:   chatResp.Portal,
		ChatInfo: chatResp.PortalInfo,
	})
	if err != nil {
		t.Fatalf("first ensurePortalRoom returned error: %v", err)
	}
	if _, err = client.ensurePortalRoom(ctx, ensurePortalRoomParams{
		Portal: portal,
	}); err != nil {
		t.Fatalf("second ensurePortalRoom returned error: %v", err)
	}
	if ghostAPI.sendCount != 1 {
		t.Fatalf("expected one initial notice send, got %d", ghostAPI.sendCount)
	}
	if botAPI.sendCount != 0 {
		t.Fatalf("expected no bridge bot sends, got %d", botAPI.sendCount)
	}
}

func TestProvisionResolveIdentifierSendsInitialWelcomeNotice(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderMagicProxy)
	client.UserLogin.Client = client

	matrix := client.UserLogin.Bridge.Matrix.(*testMatrixConnector)
	ghostAPI := &testMatrixAPI{}
	botAPI := &testMatrixAPI{createRoomID: id.RoomID("!provisioned-ai-chat:example.com")}
	matrix.api = ghostAPI
	client.UserLogin.Bridge.Bot = botAPI

	ghost, err := client.resolveChatGhost(ctx, modelUserID(client.effectiveModel(nil)))
	if err != nil {
		t.Fatalf("resolveChatGhost returned error: %v", err)
	}
	if ghost == nil {
		t.Fatal("expected ghost")
	}

	resp, err := provisionutil.ResolveIdentifier(ctx, client.UserLogin, string(ghost.ID), true)
	if err != nil {
		t.Fatalf("ResolveIdentifier returned error: %v", err)
	}
	if resp == nil || resp.Portal == nil {
		t.Fatalf("expected chat response with portal, got %#v", resp)
	}
	if resp.Portal.MXID == "" {
		t.Fatal("expected provisioning path to materialize Matrix room")
	}
	if ghostAPI.sendCount != 1 {
		t.Fatalf("expected one initial notice send, got %d", ghostAPI.sendCount)
	}
	if botAPI.sendCount != 0 {
		t.Fatalf("expected no bridge bot sends, got %d", botAPI.sendCount)
	}
}
