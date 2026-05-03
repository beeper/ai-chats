package connector

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
	uploadName   string
	uploadMime   string
	uploadData   []byte
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
func (tma *testMatrixAPI) UploadMedia(_ context.Context, _ id.RoomID, data []byte, fileName string, mimeType string) (id.ContentURIString, *event.EncryptedFileInfo, error) {
	tma.uploadName = fileName
	tma.uploadMime = mimeType
	tma.uploadData = append([]byte(nil), data...)
	return id.ContentURIString("mxc://example.com/upload"), nil, nil
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

func TestSendGeneratedMediaCanSendVoiceMessage(t *testing.T) {
	ctx := context.Background()
	oc := newDBBackedTestAIClient(t, ProviderOpenAI)
	portal := testAIModelPortal(t, oc, "openai/gpt-5.4")
	portal.MXID = id.RoomID("!voice:example.com")

	eventID, uri, err := oc.sendGeneratedMedia(ctx, portal, []byte("audio"), "audio/mpeg", "turn-1", event.MsgAudio, "reply.mp3", BeeperAIKey, true, "")
	if err != nil {
		t.Fatalf("send generated voice: %v", err)
	}
	if uri != "mxc://example.com/upload" {
		t.Fatalf("unexpected send result event=%q uri=%q", eventID, uri)
	}
	api := oc.UserLogin.Bridge.Matrix.(*testMatrixConnector).api
	if api.uploadName != "reply.mp3" || api.uploadMime != "audio/mpeg" || string(api.uploadData) != "audio" {
		t.Fatalf("unexpected uploaded voice: name=%q mime=%q data=%q", api.uploadName, api.uploadMime, string(api.uploadData))
	}
}

func TestPopulateAudioMessageContentMarksVoice(t *testing.T) {
	content := &event.MessageEventContent{MsgType: event.MsgAudio}
	populateAudioMessageContent(content, []byte("audio"), "audio/mpeg", true, event.MsgAudio)
	if content.MsgType != event.MsgAudio {
		t.Fatalf("expected audio message, got %q", content.MsgType)
	}
	if content.MSC3245Voice == nil {
		t.Fatalf("expected Matrix voice metadata")
	}
}
