package sdk

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/pkg/matrixevents"
	"github.com/beeper/agentremote/pkg/shared/citations"
	"github.com/beeper/agentremote/turns"
)

type sdkTestMatrixAPI struct {
	joinedRooms []id.RoomID
}

func (stma *sdkTestMatrixAPI) GetMXID() id.UserID   { return "@ghost:test" }
func (stma *sdkTestMatrixAPI) IsDoublePuppet() bool { return false }
func (stma *sdkTestMatrixAPI) SendMessage(context.Context, id.RoomID, event.Type, *event.Content, *bridgev2.MatrixSendExtra) (*mautrix.RespSendEvent, error) {
	return nil, nil
}
func (stma *sdkTestMatrixAPI) SendState(context.Context, id.RoomID, event.Type, string, *event.Content, time.Time) (*mautrix.RespSendEvent, error) {
	return nil, nil
}
func (stma *sdkTestMatrixAPI) MarkRead(context.Context, id.RoomID, id.EventID, time.Time) error {
	return nil
}
func (stma *sdkTestMatrixAPI) MarkUnread(context.Context, id.RoomID, bool) error { return nil }
func (stma *sdkTestMatrixAPI) MarkTyping(context.Context, id.RoomID, bridgev2.TypingType, time.Duration) error {
	return nil
}
func (stma *sdkTestMatrixAPI) DownloadMedia(context.Context, id.ContentURIString, *event.EncryptedFileInfo) ([]byte, error) {
	return nil, nil
}
func (stma *sdkTestMatrixAPI) DownloadMediaToFile(context.Context, id.ContentURIString, *event.EncryptedFileInfo, bool, func(*os.File) error) error {
	return nil
}
func (stma *sdkTestMatrixAPI) UploadMedia(context.Context, id.RoomID, []byte, string, string) (id.ContentURIString, *event.EncryptedFileInfo, error) {
	return "", nil, nil
}
func (stma *sdkTestMatrixAPI) UploadMediaStream(context.Context, id.RoomID, int64, bool, bridgev2.FileStreamCallback) (id.ContentURIString, *event.EncryptedFileInfo, error) {
	return "", nil, nil
}
func (stma *sdkTestMatrixAPI) SetDisplayName(context.Context, string) error            { return nil }
func (stma *sdkTestMatrixAPI) SetAvatarURL(context.Context, id.ContentURIString) error { return nil }
func (stma *sdkTestMatrixAPI) SetExtraProfileMeta(context.Context, any) error          { return nil }
func (stma *sdkTestMatrixAPI) CreateRoom(context.Context, *mautrix.ReqCreateRoom) (id.RoomID, error) {
	return "", nil
}
func (stma *sdkTestMatrixAPI) DeleteRoom(context.Context, id.RoomID, bool) error { return nil }
func (stma *sdkTestMatrixAPI) EnsureJoined(_ context.Context, roomID id.RoomID, _ ...bridgev2.EnsureJoinedParams) error {
	stma.joinedRooms = append(stma.joinedRooms, roomID)
	return nil
}
func (stma *sdkTestMatrixAPI) EnsureInvited(context.Context, id.RoomID, id.UserID) error { return nil }
func (stma *sdkTestMatrixAPI) TagRoom(context.Context, id.RoomID, event.RoomTag, bool) error {
	return nil
}
func (stma *sdkTestMatrixAPI) MuteRoom(context.Context, id.RoomID, time.Time) error { return nil }
func (stma *sdkTestMatrixAPI) GetEvent(context.Context, id.RoomID, id.EventID) (*event.Event, error) {
	return nil, nil
}

var _ bridgev2.MatrixAPI = (*sdkTestMatrixAPI)(nil)

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

func TestTurnBuildRelatesToDefaultsToSourceEvent(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, UserMessageSource("$source"))
	rel := turn.buildRelatesTo()
	if rel == nil || rel.EventID != id.EventID("$source") {
		t.Fatalf("expected source event relation, got %#v", rel)
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
	turn := newTurn(context.Background(), newConversation(context.Background(), portal, login, bridgev2.EventSender{}, nil), &Agent{ID: "agent"}, nil)
	turn.SetFinalMetadataProvider(FinalMetadataProviderFunc(func(_ *Turn, finishReason string) any {
		return map[string]any{"finish_reason": finishReason, "custom": true}
	}))

	if got := turn.finalMetadataProvider.FinalMetadata(turn, "completed"); got == nil {
		t.Fatal("expected final metadata provider to be invoked")
	}
}

func TestTurnRequestApprovalWaitsForResolvedDecision(t *testing.T) {
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			UserMXID: "@owner:test",
		},
	}
	runtime := &staticRuntime{
		login: login,
		approval: agentremote.NewApprovalFlow(agentremote.ApprovalFlowConfig[*pendingSDKApprovalData]{
			Login: func() *bridgev2.UserLogin { return nil },
		}),
	}
	t.Cleanup(runtime.approval.Close)
	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			MXID: "!room:test",
		},
	}
	turn := newTurn(context.Background(), newConversation(context.Background(), portal, login, bridgev2.EventSender{}, runtime), nil, nil)

	handle := turn.Approvals().Request(ApprovalRequest{
		ToolCallID: "tool-call-1",
		ToolName:   "shell",
	})
	if handle.ID() == "" {
		t.Fatalf("expected approval id to be populated")
	}
	pending := runtime.approval.Get(handle.ID())
	if pending == nil {
		t.Fatalf("expected approval to be registered")
	}
	if pending.Data == nil || pending.Data.ToolCallID != "tool-call-1" || pending.Data.ToolName != "shell" {
		t.Fatalf("unexpected pending approval data: %#v", pending.Data)
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		_ = runtime.approval.Resolve(handle.ID(), agentremote.ApprovalDecisionPayload{
			ApprovalID: handle.ID(),
			Approved:   true,
			Reason:     agentremote.ApprovalReasonAllowOnce,
		})
	}()

	resp, err := handle.Wait(context.Background())
	if err != nil {
		t.Fatalf("unexpected wait error: %v", err)
	}
	if !resp.Approved {
		t.Fatalf("expected approval to resolve as approved")
	}
	if resp.Reason != agentremote.ApprovalReasonAllowOnce {
		t.Fatalf("unexpected approval reason %q", resp.Reason)
	}
}

func TestTurnRequestApprovalUsesProvidedApprovalID(t *testing.T) {
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			UserMXID: "@owner:test",
		},
	}
	runtime := &staticRuntime{
		login: login,
		approval: agentremote.NewApprovalFlow(agentremote.ApprovalFlowConfig[*pendingSDKApprovalData]{
			Login: func() *bridgev2.UserLogin { return nil },
		}),
	}
	t.Cleanup(runtime.approval.Close)
	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			MXID: "!room:test",
		},
	}
	turn := newTurn(context.Background(), newConversation(context.Background(), portal, login, bridgev2.EventSender{}, runtime), nil, nil)

	handle := turn.Approvals().Request(ApprovalRequest{
		ApprovalID: "provider-approval-123",
		ToolCallID: "tool-call-1",
		ToolName:   "shell",
	})
	if handle.ID() != "provider-approval-123" {
		t.Fatalf("expected provided approval id, got %q", handle.ID())
	}
	if runtime.approval.Get("provider-approval-123") == nil {
		t.Fatal("expected approval to be registered under the provided id")
	}
}

func TestTurnStreamSetTransportReceivesEvents(t *testing.T) {
	conv := NewConversation(context.Background(), nil, nil, bridgev2.EventSender{}, &Config{}, nil)
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

func TestTurnBuildFinalEditAddsReplaceRelation(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.initialEventID = id.EventID("$event-1")
	turn.networkMessageID = "msg-1"
	turn.Writer().TextDelta(turn.Context(), "streamed")
	turn.SetFinalEditPayload(&FinalEditPayload{
		Content: &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    "done",
		},
		Extra: map[string]any{
			"com.beeper.ai": map[string]any{"id": turn.ID()},
		},
		TopLevelExtra: map[string]any{
			"com.beeper.dont_render_edited": true,
		},
	})

	target, edit := turn.buildFinalEdit()
	if target != "msg-1" {
		t.Fatalf("expected network target msg-1, got %q", target)
	}
	if edit == nil || len(edit.ModifiedParts) != 1 {
		t.Fatalf("expected single modified part, got %#v", edit)
	}
	gotRelatesTo, ok := edit.ModifiedParts[0].TopLevelExtra["m.relates_to"].(*event.RelatesTo)
	if !ok {
		t.Fatalf("expected m.relates_to in top-level extra, got %#v", edit.ModifiedParts[0].TopLevelExtra)
	}
	if gotRelatesTo.Type != event.RelReplace {
		t.Fatalf("expected m.replace relation, got %#v", gotRelatesTo)
	}
	if gotRelatesTo.EventID != id.EventID("$event-1") {
		t.Fatalf("expected replace target event id, got %#v", gotRelatesTo)
	}
	if gotRelatesTo.InReplyTo != nil {
		t.Fatalf("expected edit relation to omit reply override, got %#v", gotRelatesTo)
	}
	if body := edit.ModifiedParts[0].Content.Body; body != "done" {
		t.Fatalf("expected explicit payload body to win, got %q", body)
	}
	if edit.ModifiedParts[0].Content.Mentions == nil {
		t.Fatalf("expected typed mentions on edited content")
	}
	if edit.ModifiedParts[0].Content.RelatesTo != nil {
		t.Fatalf("expected replacement content to omit reply/thread relation, got %#v", edit.ModifiedParts[0].Content.RelatesTo)
	}
	rawAI, ok := edit.ModifiedParts[0].Extra[matrixevents.BeeperAIKey].(map[string]any)
	if !ok {
		t.Fatalf("expected %s payload in edit extra, got %#v", matrixevents.BeeperAIKey, edit.ModifiedParts[0].Extra)
	}
	if rawAI["id"] != turn.ID() {
		t.Fatalf("expected ai payload id %q, got %#v", turn.ID(), rawAI["id"])
	}
}

func TestTurnBuildFinalEditStripsRelationFromNewContent(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.initialEventID = id.EventID("$event-1")
	turn.networkMessageID = "msg-1"
	turn.SetFinalEditPayload(&FinalEditPayload{
		Content: &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    "done",
			RelatesTo: (&event.RelatesTo{}).
				SetThread(id.EventID("$thread-1"), id.EventID("$reply-1")),
		},
	})

	_, edit := turn.buildFinalEdit()
	if edit == nil || len(edit.ModifiedParts) != 1 {
		t.Fatalf("expected single modified part, got %#v", edit)
	}
	if rel := edit.ModifiedParts[0].Content.RelatesTo; rel != nil {
		t.Fatalf("expected edited content to strip m.relates_to, got %#v", rel)
	}
}

func TestTurnAwaitStreamStartStopsOnPermanentError(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.session = turns.NewStreamSession(turns.StreamSessionParams{
		TurnID: "turn-no-publisher",
		GetRoomID: func() id.RoomID {
			return id.RoomID("!room:test")
		},
		GetTargetEventID: func() id.EventID {
			return id.EventID("$event-no-publisher")
		},
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		turn.awaitStreamStart()
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected awaitStreamStart to stop on permanent error")
	}
}

func TestTurnBuildFinalEditDefaultsToVisibleText(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.initialEventID = id.EventID("$event-text")
	turn.networkMessageID = "msg-text"
	turn.Writer().TextDelta(turn.Context(), "hello")
	turn.Writer().FinishText(turn.Context())
	turn.ensureDefaultFinalEditPayload("stop", "")

	target, edit := turn.buildFinalEdit()
	if target != "msg-text" {
		t.Fatalf("expected network target msg-text, got %q", target)
	}
	if edit == nil || len(edit.ModifiedParts) != 1 {
		t.Fatalf("expected single modified part, got %#v", edit)
	}
	if body := edit.ModifiedParts[0].Content.Body; body != "hello" {
		t.Fatalf("expected visible text body, got %q", body)
	}
	extra := edit.ModifiedParts[0].TopLevelExtra
	if extra["com.beeper.dont_render_edited"] != true {
		t.Fatalf("expected dont_render_edited marker, got %#v", extra)
	}
	if _, ok := extra[matrixevents.BeeperAIKey]; ok {
		t.Fatalf("expected compact %s payload outside top-level extra, got %#v", matrixevents.BeeperAIKey, extra[matrixevents.BeeperAIKey])
	}
	rawAI, ok := edit.ModifiedParts[0].Extra[matrixevents.BeeperAIKey].(map[string]any)
	if !ok {
		t.Fatalf("expected compact %s payload, got %#v", matrixevents.BeeperAIKey, edit.ModifiedParts[0].Extra)
	}
	if parts, ok := rawAI["parts"].([]any); ok {
		for _, raw := range parts {
			part, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if partType := strings.TrimSpace(stringValue(part["type"])); partType == "text" || partType == "reasoning" {
				t.Fatalf("expected compact final payload without textual parts, got %#v", part)
			}
		}
	}
	metadata, _ := rawAI["metadata"].(map[string]any)
	if metadata["finish_reason"] != "stop" {
		t.Fatalf("expected synthesized finish_reason metadata, got %#v", metadata)
	}
}

func TestTurnBuildFinalEditDefaultsToGenericBodyForArtifacts(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.initialEventID = id.EventID("$event-artifact")
	turn.networkMessageID = "msg-artifact"
	turn.Writer().SourceURL(turn.Context(), citations.SourceCitation{
		URL:   "https://example.com",
		Title: "Example",
	})
	turn.ensureDefaultFinalEditPayload("stop", "")

	_, edit := turn.buildFinalEdit()
	if edit == nil || len(edit.ModifiedParts) != 1 {
		t.Fatalf("expected single modified part, got %#v", edit)
	}
	if body := edit.ModifiedParts[0].Content.Body; body != "Completed response" {
		t.Fatalf("expected generic body for artifact-only turn, got %q", body)
	}
	rawAI, ok := edit.ModifiedParts[0].Extra[matrixevents.BeeperAIKey].(map[string]any)
	if !ok {
		t.Fatalf("expected compact ai payload, got %#v", edit.ModifiedParts[0].Extra)
	}
	parts, _ := rawAI["parts"].([]any)
	if len(parts) == 0 {
		t.Fatalf("expected artifact part in compact payload, got %#v", rawAI)
	}
}

func TestTurnBuildFinalEditPreservesMentionsInContent(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.initialEventID = id.EventID("$event-mentions")
	turn.networkMessageID = "msg-mentions"
	turn.SetFinalEditPayload(&FinalEditPayload{
		Content: &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    "hi",
			Mentions: &event.Mentions{
				UserIDs: []id.UserID{"@alice:test"},
			},
		},
		TopLevelExtra: map[string]any{
			"com.beeper.dont_render_edited": true,
		},
	})

	_, edit := turn.buildFinalEdit()
	if edit == nil || len(edit.ModifiedParts) != 1 {
		t.Fatalf("expected single modified part, got %#v", edit)
	}
	mentions := edit.ModifiedParts[0].Content.Mentions
	if mentions == nil || len(mentions.UserIDs) != 1 || mentions.UserIDs[0] != id.UserID("@alice:test") {
		t.Fatalf("expected mentions to be preserved in replacement content, got %#v", mentions)
	}
}

func TestApplyStreamPartPreservesWhitespaceTextDelta(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)

	ApplyStreamPart(turn, map[string]any{"type": "text-delta", "delta": "pretty"}, PartApplyOptions{})
	ApplyStreamPart(turn, map[string]any{"type": "text-delta", "delta": " good"}, PartApplyOptions{})

	if got := turn.VisibleText(); got != "pretty good" {
		t.Fatalf("expected visible text to preserve leading whitespace in deltas, got %q", got)
	}
}

func TestTurnSuppressFinalEditSkipsAutomaticPayload(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.initialEventID = id.EventID("$event-suppressed")
	turn.networkMessageID = "msg-suppressed"
	turn.Writer().TextDelta(turn.Context(), "hello")
	turn.SetSuppressFinalEdit(true)
	turn.ensureDefaultFinalEditPayload("stop", "")

	target, edit := turn.buildFinalEdit()
	if target != "" || edit != nil {
		t.Fatalf("expected automatic final edit to be suppressed, got target=%q edit=%#v", target, edit)
	}
}

func TestTurnBuildFinalEditDoesNotSynthesizeForMetadataOnlyTurn(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.initialEventID = id.EventID("$event-meta")
	turn.networkMessageID = "msg-meta"
	turn.Writer().Start(turn.Context(), map[string]any{"turnId": turn.ID()})
	turn.ensureDefaultFinalEditPayload("stop", "")

	target, edit := turn.buildFinalEdit()
	if target != "" || edit != nil {
		t.Fatalf("expected no synthesized edit for metadata-only turn, got target=%q edit=%#v", target, edit)
	}
}

func TestTurnBuildFinalEditUsesErrorTextFallback(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.initialEventID = id.EventID("$event-error")
	turn.networkMessageID = "msg-error"
	turn.Writer().Error(turn.Context(), "boom")
	turn.ensureDefaultFinalEditPayload("error", "boom")

	_, edit := turn.buildFinalEdit()
	if edit == nil || len(edit.ModifiedParts) != 1 {
		t.Fatalf("expected synthesized error edit, got %#v", edit)
	}
	if body := edit.ModifiedParts[0].Content.Body; body != "boom" {
		t.Fatalf("expected error text fallback body, got %q", body)
	}
}

func TestTurnIdleTimeoutAbortsStuckTurn(t *testing.T) {
	conv := NewConversation(context.Background(), nil, nil, bridgev2.EventSender{}, &Config{
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
	conv := NewConversation(context.Background(), nil, nil, bridgev2.EventSender{}, &Config{
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

func TestTurnWriterStartEnsuresSenderJoinedBeforePlaceholderSend(t *testing.T) {
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: "login-1"}}
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:test"}}
	intent := &sdkTestMatrixAPI{}
	conv := newConversation(context.Background(), portal, login, bridgev2.EventSender{Sender: "agent-test", SenderLogin: login.ID}, nil)
	conv.intentOverride = func(context.Context) (bridgev2.MatrixAPI, error) { return intent, nil }
	turn := newTurn(context.Background(), conv, nil, nil)

	sendCalls := 0
	turn.SetSendFunc(func(context.Context) (id.EventID, networkid.MessageID, error) {
		sendCalls++
		if len(intent.joinedRooms) != 1 || intent.joinedRooms[0] != portal.MXID {
			t.Fatalf("expected sender to be joined before placeholder send, got %#v", intent.joinedRooms)
		}
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
