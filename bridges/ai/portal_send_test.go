package ai

import (
	"context"
	"os"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"maunium.net/go/mautrix"
)

type testMatrixAPI struct {
	joinedRooms  []id.RoomID
	sentRoomID   id.RoomID
	sentType     event.Type
	sentContent  *event.Content
	createRoomID id.RoomID
	sendCount    int
}

func (tma *testMatrixAPI) GetMXID() id.UserID   { return "@ghost:test" }
func (tma *testMatrixAPI) IsDoublePuppet() bool { return false }
func (tma *testMatrixAPI) SendMessage(_ context.Context, roomID id.RoomID, evtType event.Type, content *event.Content, _ *bridgev2.MatrixSendExtra) (*mautrix.RespSendEvent, error) {
	tma.sendCount++
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
	if tma.createRoomID != "" {
		return tma.createRoomID, nil
	}
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
