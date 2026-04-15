package ai

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2/networkid"
)

func TestGetChatInfoUsesRichProjectionForAgentChats(t *testing.T) {
	ctx := context.Background()
	client := newResponderMetadataTestClient(t)
	store := &AgentStoreAdapter{client: client}
	agent, err := store.GetAgentByID(ctx, "custom-agent")
	if err != nil {
		t.Fatalf("GetAgentByID returned error: %v", err)
	}

	resp, err := client.createChat(ctx, chatCreateParams{
		ModelID: "openai/gpt-5-mini",
		Agent:   agent,
	})
	if err != nil {
		t.Fatalf("createChat returned error: %v", err)
	}

	info, err := client.GetChatInfo(ctx, resp.Portal)
	if err != nil {
		t.Fatalf("GetChatInfo returned error: %v", err)
	}
	if info == nil || info.Members == nil {
		t.Fatalf("expected rich chat info with members, got %#v", info)
	}
	agentGhostID := networkid.UserID(agentUserIDForLogin(client.UserLogin.ID, "custom-agent"))
	if info.Members.OtherUserID != agentGhostID {
		t.Fatalf("expected agent ghost %q, got %q", agentGhostID, info.Members.OtherUserID)
	}
	if _, ok := info.Members.MemberMap[agentGhostID]; !ok {
		t.Fatalf("expected agent ghost member in room info")
	}
	if _, ok := info.Members.MemberMap[modelUserID("openai/gpt-5-mini")]; ok {
		t.Fatalf("expected agent room projection to replace model ghost member")
	}
	if info.ExtraUpdates == nil {
		t.Fatalf("expected chat projection to preserve bridgev2 room extra updates")
	}
}

func TestGetChatInfoKeepsInternalRoomsAsFallbackProjection(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, "")
	portal, err := client.UserLogin.Bridge.GetPortalByKey(ctx, networkid.PortalKey{
		ID:       networkid.PortalID("heartbeat:test-agent"),
		Receiver: client.UserLogin.ID,
	})
	if err != nil {
		t.Fatalf("GetPortalByKey returned error: %v", err)
	}
	portal.Name = "Heartbeat: test-agent"
	portal.Metadata = &PortalMetadata{
		InternalRoomKind: "heartbeat",
		Slug:             "heartbeat:test-agent",
	}

	info, err := client.GetChatInfo(ctx, portal)
	if err != nil {
		t.Fatalf("GetChatInfo returned error: %v", err)
	}
	if info == nil || info.Name == nil || *info.Name != "Heartbeat: test-agent" {
		t.Fatalf("expected fallback room name, got %#v", info)
	}
	if info.Members != nil {
		t.Fatalf("expected internal rooms to keep fallback projection, got %#v", info.Members)
	}
}
