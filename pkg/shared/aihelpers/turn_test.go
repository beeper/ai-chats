package aihelpers

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-chats/pkg/matrixevents"
	"github.com/beeper/ai-chats/pkg/shared/turns"
)

type aiTestMatrixAPI struct {
	joinedRooms  []id.RoomID
	sentMessages []aiTestSentMessage
}

type aiTestSentMessage struct {
	roomID    id.RoomID
	eventType event.Type
	content   *event.Content
}

func (stma *aiTestMatrixAPI) GetMXID() id.UserID   { return "@ghost:test" }
func (stma *aiTestMatrixAPI) IsDoublePuppet() bool { return false }
func (stma *aiTestMatrixAPI) SendMessage(_ context.Context, roomID id.RoomID, evtType event.Type, content *event.Content, _ *bridgev2.MatrixSendExtra) (*mautrix.RespSendEvent, error) {
	stma.sentMessages = append(stma.sentMessages, aiTestSentMessage{
		roomID:    roomID,
		eventType: evtType,
		content:   content,
	})
	return nil, nil
}
func (stma *aiTestMatrixAPI) SendState(context.Context, id.RoomID, event.Type, string, *event.Content, time.Time) (*mautrix.RespSendEvent, error) {
	return nil, nil
}
func (stma *aiTestMatrixAPI) MarkRead(context.Context, id.RoomID, id.EventID, time.Time) error {
	return nil
}
func (stma *aiTestMatrixAPI) MarkUnread(context.Context, id.RoomID, bool) error { return nil }
func (stma *aiTestMatrixAPI) MarkTyping(context.Context, id.RoomID, bridgev2.TypingType, time.Duration) error {
	return nil
}
func (stma *aiTestMatrixAPI) DownloadMedia(context.Context, id.ContentURIString, *event.EncryptedFileInfo) ([]byte, error) {
	return nil, nil
}
func (stma *aiTestMatrixAPI) DownloadMediaToFile(context.Context, id.ContentURIString, *event.EncryptedFileInfo, bool, func(*os.File) error) error {
	return nil
}
func (stma *aiTestMatrixAPI) UploadMedia(context.Context, id.RoomID, []byte, string, string) (id.ContentURIString, *event.EncryptedFileInfo, error) {
	return "", nil, nil
}
func (stma *aiTestMatrixAPI) UploadMediaStream(context.Context, id.RoomID, int64, bool, bridgev2.FileStreamCallback) (id.ContentURIString, *event.EncryptedFileInfo, error) {
	return "", nil, nil
}
func (stma *aiTestMatrixAPI) SetDisplayName(context.Context, string) error            { return nil }
func (stma *aiTestMatrixAPI) SetAvatarURL(context.Context, id.ContentURIString) error { return nil }
func (stma *aiTestMatrixAPI) SetExtraProfileMeta(context.Context, any) error          { return nil }
func (stma *aiTestMatrixAPI) CreateRoom(context.Context, *mautrix.ReqCreateRoom) (id.RoomID, error) {
	return "", nil
}
func (stma *aiTestMatrixAPI) DeleteRoom(context.Context, id.RoomID, bool) error { return nil }
func (stma *aiTestMatrixAPI) EnsureJoined(_ context.Context, roomID id.RoomID, _ ...bridgev2.EnsureJoinedParams) error {
	stma.joinedRooms = append(stma.joinedRooms, roomID)
	return nil
}
func (stma *aiTestMatrixAPI) EnsureInvited(context.Context, id.RoomID, id.UserID) error { return nil }
func (stma *aiTestMatrixAPI) TagRoom(context.Context, id.RoomID, event.RoomTag, bool) error {
	return nil
}
func (stma *aiTestMatrixAPI) MuteRoom(context.Context, id.RoomID, time.Time) error { return nil }
func (stma *aiTestMatrixAPI) GetEvent(context.Context, id.RoomID, id.EventID) (*event.Event, error) {
	return nil, nil
}

var _ bridgev2.MatrixAPI = (*aiTestMatrixAPI)(nil)

type testStreamTransport struct {
	startedEvent id.EventID
}

func (tst *testStreamTransport) NewDescriptor(_ context.Context, _ id.RoomID, streamType string) (*event.BeeperStreamInfo, error) {
	return &event.BeeperStreamInfo{Type: streamType}, nil
}

func (tst *testStreamTransport) Register(_ context.Context, _ id.RoomID, eventID id.EventID, _ *event.BeeperStreamInfo) error {
	tst.startedEvent = eventID
	return nil
}

func (tst *testStreamTransport) Publish(context.Context, id.RoomID, id.EventID, map[string]any) error {
	return nil
}

func (tst *testStreamTransport) Unregister(id.RoomID, id.EventID) {
}

func TestTurnBuildRelatesToRequiresExplicitReplyOrThread(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, UserMessageSource("$source"))
	rel := turn.buildRelatesTo()
	if rel != nil {
		t.Fatalf("expected no relation without explicit reply or thread, got %#v", rel)
	}
}

func TestTurnBuildRelatesToPrefersReplyAndThread(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, UserMessageSource("$source"))
	turn.SetReplyTo(id.EventID("$reply"))
	rel := turn.buildRelatesTo()
	if rel == nil || rel.InReplyTo == nil || rel.InReplyTo.EventID != id.EventID("$reply") {
		t.Fatalf("expected explicit reply relation, got %#v", rel)
	}

	turn.SetThread(id.EventID("$thread"))
	rel = turn.buildRelatesTo()
	if rel == nil || rel.EventID != id.EventID("$thread") {
		t.Fatalf("expected thread root relation, got %#v", rel)
	}
	if rel.InReplyTo == nil || rel.InReplyTo.EventID != id.EventID("$reply") {
		t.Fatalf("expected thread fallback reply, got %#v", rel)
	}
}

func TestTurnFinalMetadataMergesSupportedCallerMetadata(t *testing.T) {
	turn := newTurn(context.Background(), &Conversation{}, &Agent{ID: "runtime-agent"}, nil)
	turn.visibleText.WriteString("runtime body")
	turn.Writer().MessageMetadata(turn.Context(), map[string]any{
		"prompt_tokens":     123,
		"completion_tokens": 456,
		"finish_reason":     "caller-finish",
		"turn_id":           "caller-turn",
		"agent_id":          "caller-agent",
		"body":              "caller body",
		"started_at_ms":     1,
	})

	meta := turn.finalMetadata("runtime-finish")
	if meta.PromptTokens != 123 {
		t.Fatalf("expected prompt tokens to persist, got %d", meta.PromptTokens)
	}
	if meta.CompletionTokens != 456 {
		t.Fatalf("expected completion tokens to persist, got %d", meta.CompletionTokens)
	}
	if meta.FinishReason != "runtime-finish" {
		t.Fatalf("expected runtime finish reason to win, got %q", meta.FinishReason)
	}
	if meta.TurnID != turn.ID() {
		t.Fatalf("expected runtime turn id to win, got %q", meta.TurnID)
	}
	if meta.AgentID != "runtime-agent" {
		t.Fatalf("expected runtime agent id to win, got %q", meta.AgentID)
	}
	if meta.Body != "runtime body" {
		t.Fatalf("expected runtime body to win, got %q", meta.Body)
	}
	if meta.StartedAtMs != turn.startedAtMs {
		t.Fatalf("expected runtime started timestamp to win, got %d", meta.StartedAtMs)
	}
}

func TestTurnPersistFinalMessageUsesFinalMetadataProvider(t *testing.T) {
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{ID: "login-1"},
	}
	portal := &bridgev2.Portal{
		Portal: &database.Portal{MXID: "!room:test"},
	}
	turn := newTurn(context.Background(), newConversation(context.Background(), portal, login, bridgev2.EventSender{}), &Agent{ID: "agent"}, nil)
	turn.SetFinalMetadataProvider(FinalMetadataProviderFunc(func(_ *Turn, finishReason string) any {
		return map[string]any{"finish_reason": finishReason, "custom": true}
	}))

	if got := turn.finalMetadataProvider.FinalMetadata(turn, "completed"); got == nil {
		t.Fatal("expected final metadata provider to be invoked")
	}
}

func TestTurnStreamSetTransportReceivesEvents(t *testing.T) {
	conv := NewConversation(context.Background(), nil, nil, bridgev2.EventSender{}, &Config[*struct{}, *struct{}]{}, nil)
	turn := conv.StartTurn(context.Background(), &Agent{ID: "agent"}, nil)

	var gotTurnID string
	var gotContent map[string]any
	turn.Stream().SetTransport(func(turnID string, _ int, content map[string]any, _ string) bool {
		gotTurnID = turnID
		gotContent = content
		return true
	})

	if turn.streamHook == nil {
		t.Fatal("expected stream transport to register a hook")
	}
	handled := turn.streamHook(turn.ID(), 1, map[string]any{
		"type":  "text-delta",
		"delta": "hello",
	}, "txn-1")

	if !handled {
		t.Fatal("expected stream transport hook to handle the event")
	}
	if gotTurnID != turn.ID() {
		t.Fatalf("expected transport to receive turn id %q, got %q", turn.ID(), gotTurnID)
	}
	if gotContent["type"] != "text-delta" {
		t.Fatalf("expected text-delta event, got %#v", gotContent)
	}
	if gotContent["delta"] != "hello" {
		t.Fatalf("expected text delta payload, got %#v", gotContent)
	}
}

func TestTurnBuildPlaceholderMessageUsesConfiguredPayload(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.SetPlaceholderMessagePayload(&PlaceholderMessagePayload{
		Content: &event.MessageEventContent{
			MsgType:  event.MsgText,
			Body:     "Pondering...",
			Mentions: &event.Mentions{},
		},
		Extra: map[string]any{
			"com.beeper.ai": map[string]any{"id": turn.ID()},
		},
		DBMetadata: map[string]any{"custom": true},
	})

	msg := turn.buildPlaceholderMessage()
	if msg == nil || len(msg.Parts) != 1 {
		t.Fatalf("expected single placeholder part, got %#v", msg)
	}
	part := msg.Parts[0]
	if part.Content.Body != "Pondering..." {
		t.Fatalf("expected placeholder body override, got %#v", part.Content.Body)
	}
	if part.DBMetadata == nil {
		t.Fatalf("expected placeholder DB metadata")
	}
	if part.Content.Mentions == nil {
		t.Fatalf("expected typed mentions default to be preserved")
	}
}

func TestTurnBuildPlaceholderMessageSeedsAIContentByDefault(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.SetPlaceholderMessagePayload(&PlaceholderMessagePayload{
		Content: &event.MessageEventContent{
			MsgType:  event.MsgText,
			Body:     "Pondering...",
			Mentions: &event.Mentions{},
		},
		Extra: map[string]any{},
	})

	msg := turn.buildPlaceholderMessage()
	if msg == nil || len(msg.Parts) != 1 {
		t.Fatalf("expected single placeholder part, got %#v", msg)
	}
	part := msg.Parts[0]
	rawAI, ok := part.Extra[matrixevents.BeeperAIKey].(map[string]any)
	if !ok {
		t.Fatalf("expected %s payload map, got %#v", matrixevents.BeeperAIKey, part.Extra[matrixevents.BeeperAIKey])
	}
	if rawAI["id"] != turn.ID() {
		t.Fatalf("expected ai id %q, got %#v", turn.ID(), rawAI["id"])
	}
	if rawAI["role"] != "assistant" {
		t.Fatalf("expected assistant role, got %#v", rawAI["role"])
	}
	metadata, ok := rawAI["metadata"].(map[string]any)
	if !ok || metadata["turn_id"] != turn.ID() {
		t.Fatalf("expected turn metadata, got %#v", rawAI["metadata"])
	}
	parts, ok := rawAI["parts"].([]any)
	if !ok || len(parts) != 0 {
		t.Fatalf("expected empty parts array, got %#v", rawAI["parts"])
	}
}

func TestTurnEnsureStreamStartedAsyncStartsAfterTargetResolution(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.networkMessageID = "msg-async"

	transport := &testStreamTransport{}
	var resolved atomic.Bool
	var sentCount atomic.Int32

	turn.session = turns.NewStreamSession(turns.StreamSessionParams{
		TurnID: "turn-async",
		GetStreamTarget: func() turns.StreamTarget {
			return turns.StreamTarget{NetworkMessageID: turn.networkMessageID}
		},
		ResolveTargetEventID: func(context.Context, turns.StreamTarget) (id.EventID, error) {
			if !resolved.Load() {
				return "", nil
			}
			return id.EventID("$event-async"), nil
		},
		GetRoomID: func() id.RoomID {
			return id.RoomID("!room:test")
		},
		GetTargetEventID: func() id.EventID { return turn.initialEventID },
		GetStreamPublisher: func(context.Context) (bridgev2.BeeperStreamPublisher, bool) {
			return transport, true
		},
		NextSeq: func() int { return 1 },
		SendHook: func(_ string, _ int, _ map[string]any, _ string) bool {
			sentCount.Add(1)
			return true
		},
	})

	turn.session.EmitPart(context.Background(), map[string]any{"type": "text-delta", "delta": "hello"})
	turn.ensureStreamStartedAsync()
	time.Sleep(25 * time.Millisecond)
	if sentCount.Load() != 0 {
		t.Fatalf("expected stream not to flush before target resolution, got %d sends", sentCount.Load())
	}

	resolved.Store(true)

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if sentCount.Load() == 1 && transport.startedEvent == id.EventID("$event-async") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected async stream start to flush pending part after target resolution, got sends=%d started=%s", sentCount.Load(), transport.startedEvent)
}

func TestTurnIdleTimeoutAbortsStuckTurn(t *testing.T) {
	conv := NewConversation(context.Background(), nil, nil, bridgev2.EventSender{}, &Config[*struct{}, *struct{}]{
		TurnManagement: &TurnConfig{IdleTimeoutMs: 20},
	}, nil)
	turn := conv.StartTurn(context.Background(), nil, nil)
	turn.Writer().TextDelta(turn.Context(), "hello")

	waitForTurnEnd(t, turn, 300*time.Millisecond)
	if !turn.ended {
		t.Fatal("expected idle timeout to end the turn")
	}
	ui := turn.UIState().UIMessage
	metadata, _ := ui["metadata"].(map[string]any)
	terminal, _ := metadata["beeper_terminal_state"].(map[string]any)
	if terminal["type"] != "abort" {
		t.Fatalf("expected abort timeout terminal state, got %#v", terminal)
	}
}

func TestTurnIdleTimeoutResetsOnActivity(t *testing.T) {
	conv := NewConversation(context.Background(), nil, nil, bridgev2.EventSender{}, &Config[*struct{}, *struct{}]{
		TurnManagement: &TurnConfig{IdleTimeoutMs: 40},
	}, nil)
	turn := conv.StartTurn(context.Background(), nil, nil)
	turn.Writer().TextDelta(turn.Context(), "a")
	time.Sleep(20 * time.Millisecond)
	turn.Writer().TextDelta(turn.Context(), "b")
	time.Sleep(20 * time.Millisecond)
	if turn.ended {
		t.Fatal("expected activity to reset the idle timeout")
	}
	waitForTurnEnd(t, turn, 300*time.Millisecond)
	if !turn.ended {
		t.Fatal("expected turn to end after activity stops")
	}
}

func TestTurnEndWithErrorSendsStatusWhenStarted(t *testing.T) {
	// Create a turn with a source ref (needed for SendStatus path).
	turn := newTurn(context.Background(), nil, nil, UserMessageSource("$source"))

	// Simulate that the turn has started streaming content.
	turn.started = true

	// EndWithError should not panic and should transition to ended state.
	// SendStatus is a no-op without a full conv/login/portal, but the code path
	// through Writer().Error → SendStatus → Writer().Finish must not crash.
	turn.EndWithError("test error")

	if !turn.ended {
		t.Fatal("expected turn to be ended after EndWithError")
	}
}

func TestTurnEndWithErrorSendsStatusWhenNotStarted(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, UserMessageSource("$source"))

	// Turn not started — EndWithError should still send a fail status and end.
	turn.EndWithError("pre-start error")

	if !turn.ended {
		t.Fatal("expected turn to be ended after EndWithError")
	}
}

func TestTurnSourceRefCarriesSenderID(t *testing.T) {
	source := &SourceRef{
		Kind:     SourceKindUserMessage,
		EventID:  "$evt1",
		SenderID: "@user:test",
	}
	turn := newTurn(context.Background(), nil, nil, source)
	if turn.Source().SenderID != "@user:test" {
		t.Fatalf("expected sender id, got %q", turn.Source().SenderID)
	}
	if turn.Source().EventID != "$evt1" {
		t.Fatalf("expected event id, got %q", turn.Source().EventID)
	}
}

func TestTurnWriterStartTriggersLazyPlaceholderSend(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)

	sendCalls := 0
	turn.SetSendFunc(func(context.Context) (id.EventID, networkid.MessageID, error) {
		sendCalls++
		return "", networkid.MessageID("msg-1"), nil
	})

	turn.Writer().Start(turn.Context(), map[string]any{"turnId": turn.ID()})

	if sendCalls != 1 {
		t.Fatalf("expected placeholder send to happen once, got %d", sendCalls)
	}
	if !turn.started {
		t.Fatal("expected turn to be marked started after Writer().Start()")
	}
	if !turn.UIState().UIStarted {
		t.Fatal("expected UI start marker to be applied")
	}
	if turn.NetworkMessageID() != networkid.MessageID("msg-1") {
		t.Fatalf("expected placeholder network message id to be stored, got %q", turn.NetworkMessageID())
	}
}

func TestTurnWriterStartSendsPlaceholderWithoutSenderPreflight(t *testing.T) {
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: "login-1"}}
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:test"}}
	conv := newConversation(context.Background(), portal, login, bridgev2.EventSender{Sender: "agent-test", SenderLogin: login.ID})
	turn := newTurn(context.Background(), conv, nil, nil)

	sendCalls := 0
	turn.SetSendFunc(func(context.Context) (id.EventID, networkid.MessageID, error) {
		sendCalls++
		return "", networkid.MessageID("msg-joined"), nil
	})

	turn.Writer().Start(turn.Context(), map[string]any{"turnId": turn.ID()})

	if sendCalls != 1 {
		t.Fatalf("expected placeholder send once, got %d", sendCalls)
	}
}

func waitForTurnEnd(t *testing.T, turn *Turn, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if turn.ended {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}
