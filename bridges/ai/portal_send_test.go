package ai

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type testMatrixAPI struct {
	joinedRooms []id.RoomID
	sentRoomID  id.RoomID
	sentType    event.Type
	sentContent *event.Content
}

func (tma *testMatrixAPI) GetMXID() id.UserID   { return "@ghost:test" }
func (tma *testMatrixAPI) IsDoublePuppet() bool { return false }
func (tma *testMatrixAPI) SendMessage(_ context.Context, roomID id.RoomID, evtType event.Type, content *event.Content, _ *bridgev2.MatrixSendExtra) (*mautrix.RespSendEvent, error) {
	tma.sentRoomID = roomID
	tma.sentType = evtType
	tma.sentContent = content
	return &mautrix.RespSendEvent{EventID: "$test"}, nil
}
func (tma *testMatrixAPI) SendState(context.Context, id.RoomID, event.Type, string, *event.Content, time.Time) (*mautrix.RespSendEvent, error) {
	return nil, nil
}
func (tma *testMatrixAPI) MarkRead(context.Context, id.RoomID, id.EventID, time.Time) error {
	return nil
}
func (tma *testMatrixAPI) MarkUnread(context.Context, id.RoomID, bool) error { return nil }
func (tma *testMatrixAPI) MarkTyping(context.Context, id.RoomID, bridgev2.TypingType, time.Duration) error {
	return nil
}
func (tma *testMatrixAPI) DownloadMedia(context.Context, id.ContentURIString, *event.EncryptedFileInfo) ([]byte, error) {
	return nil, nil
}
func (tma *testMatrixAPI) DownloadMediaToFile(context.Context, id.ContentURIString, *event.EncryptedFileInfo, bool, func(*os.File) error) error {
	return nil
}
func (tma *testMatrixAPI) UploadMedia(context.Context, id.RoomID, []byte, string, string) (id.ContentURIString, *event.EncryptedFileInfo, error) {
	return "", nil, nil
}
func (tma *testMatrixAPI) UploadMediaStream(context.Context, id.RoomID, int64, bool, bridgev2.FileStreamCallback) (id.ContentURIString, *event.EncryptedFileInfo, error) {
	return "", nil, nil
}
func (tma *testMatrixAPI) SetDisplayName(context.Context, string) error            { return nil }
func (tma *testMatrixAPI) SetAvatarURL(context.Context, id.ContentURIString) error { return nil }
func (tma *testMatrixAPI) SetExtraProfileMeta(context.Context, any) error          { return nil }
func (tma *testMatrixAPI) CreateRoom(context.Context, *mautrix.ReqCreateRoom) (id.RoomID, error) {
	return "", nil
}
func (tma *testMatrixAPI) DeleteRoom(context.Context, id.RoomID, bool) error { return nil }
func (tma *testMatrixAPI) EnsureJoined(_ context.Context, roomID id.RoomID, _ ...bridgev2.EnsureJoinedParams) error {
	tma.joinedRooms = append(tma.joinedRooms, roomID)
	return nil
}
func (tma *testMatrixAPI) EnsureInvited(context.Context, id.RoomID, id.UserID) error     { return nil }
func (tma *testMatrixAPI) TagRoom(context.Context, id.RoomID, event.RoomTag, bool) error { return nil }
func (tma *testMatrixAPI) MuteRoom(context.Context, id.RoomID, time.Time) error          { return nil }
func (tma *testMatrixAPI) GetEvent(context.Context, id.RoomID, id.EventID) (*event.Event, error) {
	return nil, nil
}

var _ bridgev2.MatrixAPI = (*testMatrixAPI)(nil)

func TestSendViaPortalRejectsMissingBridgeState(t *testing.T) {
	_, _, err := (&AIClient{}).sendViaPortal(context.Background(), &bridgev2.Portal{}, &bridgev2.ConvertedMessage{}, "")
	if err == nil {
		t.Fatal("expected bridge unavailable error")
	}
	if !strings.Contains(err.Error(), "bridge unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendViaPortalRejectsInvalidPortal(t *testing.T) {
	oc := &AIClient{UserLogin: &bridgev2.UserLogin{Bridge: &bridgev2.Bridge{}}}

	_, _, err := oc.sendViaPortal(context.Background(), nil, &bridgev2.ConvertedMessage{}, "")
	if err == nil {
		t.Fatal("expected invalid portal error")
	}
	if !strings.Contains(err.Error(), "invalid portal") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendEditViaPortalRejectsMissingBridgeState(t *testing.T) {
	err := (&AIClient{}).sendEditViaPortal(context.Background(), &bridgev2.Portal{}, networkid.MessageID("msg-1"), &bridgev2.ConvertedEdit{})
	if err == nil {
		t.Fatal("expected bridge unavailable error")
	}
	if !strings.Contains(err.Error(), "bridge unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendEditViaPortalRejectsInvalidTargetMessage(t *testing.T) {
	oc := &AIClient{UserLogin: &bridgev2.UserLogin{Bridge: &bridgev2.Bridge{}}}
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:example.com"}}

	err := oc.sendEditViaPortal(context.Background(), portal, "", &bridgev2.ConvertedEdit{})
	if err == nil {
		t.Fatal("expected invalid target message error")
	}
	if !strings.Contains(err.Error(), "invalid target message") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolvePortalSenderAndIntentEnsuresJoined(t *testing.T) {
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:example.com"}}
	intent := &testMatrixAPI{}
	sender := bridgev2.EventSender{Sender: "agent-test", SenderLogin: "login-1"}

	gotSender, gotIntent, err := resolvePortalSenderAndIntent(
		context.Background(),
		portal,
		sender,
		bridgev2.RemoteEventMessage,
		true,
		func(_ context.Context, _ *bridgev2.Portal, gotSender bridgev2.EventSender, _ bridgev2.RemoteEventType) (bridgev2.MatrixAPI, error) {
			if gotSender != sender {
				t.Fatalf("expected sender %#v, got %#v", sender, gotSender)
			}
			return intent, nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotSender != sender {
		t.Fatalf("expected sender %#v, got %#v", sender, gotSender)
	}
	if gotIntent != intent {
		t.Fatalf("expected returned intent to match test intent")
	}
	if len(intent.joinedRooms) != 1 || intent.joinedRooms[0] != portal.MXID {
		t.Fatalf("expected EnsureJoined for %s, got %#v", portal.MXID, intent.joinedRooms)
	}
}

func TestSenderForPortalUsesResolvedAgentGhost(t *testing.T) {
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: "magic-proxy:@user:test"}}
	oc := &AIClient{UserLogin: login}
	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			OtherUserID: agentUserIDForLogin(login.ID, "agent-1"),
		},
	}

	sender := oc.senderForPortal(context.Background(), portal)
	if sender.Sender != agentUserIDForLogin(login.ID, "agent-1") {
		t.Fatalf("expected agent ghost sender, got %q", sender.Sender)
	}
	if sender.SenderLogin != login.ID {
		t.Fatalf("expected sender login %q, got %q", login.ID, sender.SenderLogin)
	}
}

func TestSenderForPortalUsesModelGhostWithoutAgent(t *testing.T) {
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: "magic-proxy:@user:test"}}
	oc := &AIClient{UserLogin: login}
	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			OtherUserID: modelUserID("gpt-5.4"),
		},
	}

	sender := oc.senderForPortal(context.Background(), portal)
	if sender.Sender != modelUserID("gpt-5.4") {
		t.Fatalf("expected model ghost sender, got %q", sender.Sender)
	}
	if sender.SenderLogin != login.ID {
		t.Fatalf("expected sender login %q, got %q", login.ID, sender.SenderLogin)
	}
}

func TestSendSystemNoticeUsesBridgeBot(t *testing.T) {
	bot := &testMatrixAPI{}
	oc := &AIClient{
		UserLogin: &bridgev2.UserLogin{
			Bridge: &bridgev2.Bridge{Bot: bot},
		},
	}
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:example.com"}}

	oc.sendSystemNotice(context.Background(), portal, "AI can make mistakes.")

	if bot.sentRoomID != portal.MXID {
		t.Fatalf("expected room %q, got %q", portal.MXID, bot.sentRoomID)
	}
	if bot.sentType != event.EventMessage {
		t.Fatalf("expected event type %q, got %q", event.EventMessage, bot.sentType)
	}
	if bot.sentContent == nil {
		t.Fatal("expected content to be sent")
	}
	content, ok := bot.sentContent.Parsed.(*event.MessageEventContent)
	if !ok {
		t.Fatalf("expected message content, got %#v", bot.sentContent.Parsed)
	}
	if content.MsgType != event.MsgNotice {
		t.Fatalf("expected msgtype %q, got %q", event.MsgNotice, content.MsgType)
	}
	if content.Body != "AI can make mistakes." {
		t.Fatalf("expected notice body to be preserved, got %q", content.Body)
	}
}
