package ai

import (
	"context"
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func waitForNoticeSend(t *testing.T, ghostAPI *testMatrixAPI) *event.MessageEventContent {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ghostAPI.sentContent != nil {
			msg, ok := ghostAPI.sentContent.Parsed.(*event.MessageEventContent)
			if !ok {
				t.Fatalf("expected parsed message content, got %#v", ghostAPI.sentContent.Parsed)
			}
			return msg
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected disclaimer notice to be sent")
	return nil
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

func TestEnsurePortalRoomDoesNotSendDisclaimerOnMaterialization(t *testing.T) {
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

	time.Sleep(50 * time.Millisecond)
	if ghostAPI.sendCount != 0 {
		t.Fatalf("expected no disclaimer send during materialization, got %d sends", ghostAPI.sendCount)
	}
	if botAPI.sendCount != 0 {
		t.Fatalf("expected no bridge bot sends, got %d", botAPI.sendCount)
	}
	if meta := portalMeta(portal); meta != nil && meta.DisclaimerSent {
		t.Fatalf("expected disclaimer state to remain false after materialization, got %#v", meta)
	}
}

func TestSendDisclaimerNoticeSendsOnce(t *testing.T) {
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
	if err := client.sendDisclaimerNotice(ctx, portal); err != nil {
		t.Fatalf("sendDisclaimerNotice returned error: %v", err)
	}
	msg := waitForNoticeSend(t, ghostAPI)
	if msg.MsgType != event.MsgNotice {
		t.Fatalf("expected notice message, got %q", msg.MsgType)
	}
	if !strings.Contains(msg.Body, "AI can make mistakes.") {
		t.Fatalf("expected AI disclaimer, got %q", msg.Body)
	}
	if meta := portalMeta(portal); meta == nil || !meta.DisclaimerSent {
		t.Fatalf("expected DisclaimerSent to be persisted, got %#v", meta)
	}

	if err := client.sendDisclaimerNotice(ctx, portal); err != nil {
		t.Fatalf("second sendDisclaimerNotice returned error: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if ghostAPI.sendCount != 1 {
		t.Fatalf("expected one disclaimer send, got %d", ghostAPI.sendCount)
	}
	if botAPI.sendCount != 0 {
		t.Fatalf("expected no bridge bot sends, got %d", botAPI.sendCount)
	}
}

func TestCreateChatWithGhostDoesNotSendDisclaimerDuringMaterialization(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderMagicProxy)

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

	resp, err := client.CreateChatWithGhost(ctx, ghost)
	if err != nil {
		t.Fatalf("CreateChatWithGhost returned error: %v", err)
	}
	if resp == nil || resp.Portal == nil {
		t.Fatalf("expected chat response with portal, got %#v", resp)
	}
	if resp.Portal.MXID == "" {
		t.Fatal("expected CreateChatWithGhost to materialize a Matrix room")
	}

	time.Sleep(50 * time.Millisecond)
	if ghostAPI.sendCount != 0 {
		t.Fatalf("expected no disclaimer send during materialization, got %d sends", ghostAPI.sendCount)
	}
	if botAPI.sendCount != 0 {
		t.Fatalf("expected no bridge bot sends, got %d", botAPI.sendCount)
	}
}
