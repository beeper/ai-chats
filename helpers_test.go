package agentremote

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

func TestNormalizeAIRoomTypeV2(t *testing.T) {
	cases := []struct {
		name     string
		roomType database.RoomType
		aiKind   string
		want     string
	}{
		{name: "agent dm", roomType: database.RoomTypeDM, aiKind: AIRoomKindAgent, want: "dm"},
		{name: "agent default", roomType: database.RoomTypeDefault, aiKind: AIRoomKindAgent, want: "group"},
		{name: "agent space", roomType: database.RoomTypeSpace, aiKind: AIRoomKindAgent, want: "space"},
		{name: "subagent forced group", roomType: database.RoomTypeDM, aiKind: "subagent", want: "group"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeAIRoomTypeV2(tc.roomType, tc.aiKind); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestApplyAgentRemoteBridgeInfo(t *testing.T) {
	content := &event.BridgeEventContent{}
	ApplyAgentRemoteBridgeInfo(content, "ai-codex", database.RoomTypeDM, AIRoomKindAgent)

	if content.Protocol.ID != "ai-codex" {
		t.Fatalf("expected protocol id ai-codex, got %q", content.Protocol.ID)
	}
	if content.BeeperRoomTypeV2 != "dm" {
		t.Fatalf("expected dm room type, got %q", content.BeeperRoomTypeV2)
	}
}

func TestBuildPreConvertedRemoteMessagePreservesTimingAndGeneratesID(t *testing.T) {
	ts := time.Date(2026, time.March, 12, 12, 0, 0, 0, time.UTC)
	first := BuildPreConvertedRemoteMessage(PreConvertedRemoteMessageParams{
		PortalKey:   networkid.PortalKey{},
		MsgID:       "first",
		LogKey:      "msg_id",
		Timestamp:   ts,
		StreamOrder: 10,
	})
	second := BuildPreConvertedRemoteMessage(PreConvertedRemoteMessageParams{
		PortalKey:   networkid.PortalKey{},
		MsgID:       "second",
		LogKey:      "msg_id",
		Timestamp:   ts,
		StreamOrder: 11,
	})
	generated := BuildPreConvertedRemoteMessage(PreConvertedRemoteMessageParams{
		PortalKey: networkid.PortalKey{},
		IDPrefix:  "test",
		LogKey:    "msg_id",
	})
	if first.GetStreamOrder() != 10 {
		t.Fatalf("expected first stream order 10, got %d", first.GetStreamOrder())
	}
	if second.GetStreamOrder() != 11 {
		t.Fatalf("expected second stream order 11, got %d", second.GetStreamOrder())
	}
	if second.GetStreamOrder() <= first.GetStreamOrder() {
		t.Fatalf("expected stream order to remain increasing")
	}
	if got := string(generated.ID); len(got) < len("test:") || got[:5] != "test:" {
		t.Fatalf("expected generated id with prefix, got %q", got)
	}
}

func TestBuildLoginDMChatInfoSupportsCustomMembers(t *testing.T) {
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: "login-1"}}
	humanSender := bridgev2.EventSender{Sender: "custom-human", SenderLogin: login.ID, IsFromMe: true}
	botSender := bridgev2.EventSender{Sender: "custom-bot", SenderLogin: login.ID}
	info := BuildLoginDMChatInfo(LoginDMChatInfoParams{
		Title:             "Room",
		Topic:             "Topic",
		Login:             login,
		HumanUserIDPrefix: "ignored",
		HumanSender:       &humanSender,
		BotUserID:         "bot-1",
		BotDisplayName:    "Bot",
		BotSender:         &botSender,
		BotUserInfo:       &bridgev2.UserInfo{Identifiers: []string{"bot@example.com"}},
		BotMemberEventExtra: map[string]any{
			"displayname": "Bot",
			"custom":      true,
		},
		CanBackfill: true,
	})
	if info == nil || info.Topic == nil || *info.Topic != "Topic" {
		t.Fatalf("expected topic to be preserved, got %#v", info)
	}
	if !info.CanBackfill {
		t.Fatal("expected can_backfill to be preserved")
	}
	if got := info.Members.MemberMap[HumanUserID("ignored", login.ID)].EventSender; got.Sender != humanSender.Sender {
		t.Fatalf("expected custom human sender, got %#v", got)
	}
	botMember := info.Members.MemberMap["bot-1"]
	if botMember.EventSender.Sender != botSender.Sender {
		t.Fatalf("expected custom bot sender, got %#v", botMember.EventSender)
	}
	if botMember.UserInfo == nil || len(botMember.UserInfo.Identifiers) != 1 {
		t.Fatalf("expected bot user info to be preserved, got %#v", botMember.UserInfo)
	}
	if got, _ := botMember.MemberEventExtra["custom"].(bool); !got {
		t.Fatalf("expected bot member extra to be preserved, got %#v", botMember.MemberEventExtra)
	}
}

func TestConfigureDMPortalSetsDMFields(t *testing.T) {
	portal := &bridgev2.Portal{Portal: &database.Portal{}}
	if err := ConfigureDMPortal(context.Background(), ConfigureDMPortalParams{
		Portal:      portal,
		Title:       "Room",
		Topic:       "Topic",
		OtherUserID: "bot-1",
		Save:        false,
	}); err != nil {
		t.Fatalf("ConfigureDMPortal returned error: %v", err)
	}
	if portal.RoomType != database.RoomTypeDM {
		t.Fatalf("expected DM room type, got %q", portal.RoomType)
	}
	if portal.OtherUserID != "bot-1" {
		t.Fatalf("expected other user id to be set, got %q", portal.OtherUserID)
	}
	if portal.Name != "Room" || !portal.NameSet {
		t.Fatalf("expected portal name to be set, got %q / %v", portal.Name, portal.NameSet)
	}
	if portal.Topic != "Topic" || !portal.TopicSet {
		t.Fatalf("expected portal topic to be set, got %q / %v", portal.Topic, portal.TopicSet)
	}
}
