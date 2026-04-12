package openclaw

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/shared/streamui"
	"github.com/beeper/agentremote/sdk"
)

type testMatrixAPI struct{}

func (testMatrixAPI) GetMXID() id.UserID   { return "" }
func (testMatrixAPI) IsDoublePuppet() bool { return false }
func (testMatrixAPI) SendMessage(context.Context, id.RoomID, event.Type, *event.Content, *bridgev2.MatrixSendExtra) (*mautrix.RespSendEvent, error) {
	return nil, nil
}
func (testMatrixAPI) SendState(context.Context, id.RoomID, event.Type, string, *event.Content, time.Time) (*mautrix.RespSendEvent, error) {
	return nil, nil
}
func (testMatrixAPI) MarkRead(context.Context, id.RoomID, id.EventID, time.Time) error { return nil }
func (testMatrixAPI) MarkUnread(context.Context, id.RoomID, bool) error                { return nil }
func (testMatrixAPI) MarkTyping(context.Context, id.RoomID, bridgev2.TypingType, time.Duration) error {
	return nil
}
func (testMatrixAPI) DownloadMedia(context.Context, id.ContentURIString, *event.EncryptedFileInfo) ([]byte, error) {
	return nil, nil
}
func (testMatrixAPI) DownloadMediaToFile(context.Context, id.ContentURIString, *event.EncryptedFileInfo, bool, func(*os.File) error) error {
	return nil
}
func (testMatrixAPI) UploadMedia(context.Context, id.RoomID, []byte, string, string) (id.ContentURIString, *event.EncryptedFileInfo, error) {
	return "", nil, nil
}
func (testMatrixAPI) UploadMediaStream(context.Context, id.RoomID, int64, bool, bridgev2.FileStreamCallback) (id.ContentURIString, *event.EncryptedFileInfo, error) {
	return "", nil, nil
}
func (testMatrixAPI) SetDisplayName(context.Context, string) error            { return nil }
func (testMatrixAPI) SetAvatarURL(context.Context, id.ContentURIString) error { return nil }
func (testMatrixAPI) SetExtraProfileMeta(context.Context, any) error          { return nil }
func (testMatrixAPI) CreateRoom(context.Context, *mautrix.ReqCreateRoom) (id.RoomID, error) {
	return "", nil
}
func (testMatrixAPI) DeleteRoom(context.Context, id.RoomID, bool) error { return nil }
func (testMatrixAPI) EnsureJoined(context.Context, id.RoomID, ...bridgev2.EnsureJoinedParams) error {
	return nil
}
func (testMatrixAPI) EnsureInvited(context.Context, id.RoomID, id.UserID) error     { return nil }
func (testMatrixAPI) TagRoom(context.Context, id.RoomID, event.RoomTag, bool) error { return nil }
func (testMatrixAPI) MuteRoom(context.Context, id.RoomID, time.Time) error          { return nil }
func (testMatrixAPI) GetEvent(context.Context, id.RoomID, id.EventID) (*event.Event, error) {
	return nil, nil
}

func newOpenClawTestTurn(turnID string) *sdk.Turn {
	conv := sdk.NewConversation(context.Background(), nil, nil, bridgev2.EventSender{}, &sdk.Config[*OpenClawClient, *struct{}]{}, nil)
	turn := conv.StartTurn(context.Background(), nil, nil)
	turn.SetID(turnID)
	return turn
}

func newOpenClawTestClient(states map[string]*openClawStreamState) *OpenClawClient {
	oc := &OpenClawClient{}
	oc.streamHost = sdk.NewStreamTurnHost(sdk.StreamTurnHostCallbacks[openClawStreamState]{
		GetAborter: func(s *openClawStreamState) sdk.Aborter {
			if s.turn == nil {
				return nil
			}
			return s.turn
		},
	})
	for k, v := range states {
		oc.streamHost.Lock()
		oc.streamHost.SetLocked(k, v)
		oc.streamHost.Unlock()
	}
	return oc
}

func TestComputeVisibleDeltaTracksPrefixOnly(t *testing.T) {
	oc := newOpenClawTestClient(map[string]*openClawStreamState{
		"turn-1": {turnID: "turn-1"},
	})

	if got := oc.computeVisibleDelta("turn-1", "hello"); got != "hello" {
		t.Fatalf("expected first delta to be full text, got %q", got)
	}
	if got := oc.computeVisibleDelta("turn-1", "hello world"); got != " world" {
		t.Fatalf("expected suffix delta, got %q", got)
	}
	if got := oc.computeVisibleDelta("turn-1", "hello world"); got != "" {
		t.Fatalf("expected no delta for unchanged text, got %q", got)
	}
}

func TestIsStreamActiveReflectsStatePresence(t *testing.T) {
	oc := newOpenClawTestClient(map[string]*openClawStreamState{
		"turn-2": {turnID: "turn-2"},
	})
	if !oc.isStreamActive("turn-2") {
		t.Fatal("expected active stream state")
	}
	if oc.isStreamActive("missing") {
		t.Fatal("did not expect missing stream state to be active")
	}
}

func TestBuildStreamDBMetadataIncludesToolCalls(t *testing.T) {
	oc := &OpenClawClient{}
	state := &openClawStreamState{
		turnID:     "turn-3",
		agentID:    "main",
		sessionID:  "sess-1",
		sessionKey: "agent:main:matrix-dm",
		role:       "assistant",
		turn:       newOpenClawTestTurn("turn-3"),
	}
	state.stream.ApplyPart(map[string]any{"type": "text-delta", "delta": "running"}, time.Time{})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{
		"type": "reasoning-start",
		"id":   "reasoning-1",
	})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{
		"type":  "reasoning-delta",
		"id":    "reasoning-1",
		"delta": "thinking",
	})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{
		"type": "reasoning-end",
		"id":   "reasoning-1",
	})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{
		"type":       "tool-input-available",
		"toolCallId": "call-1",
		"toolName":   "bash",
		"input":      map[string]any{"cmd": "pwd"},
	})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{
		"type":       "tool-output-available",
		"toolCallId": "call-1",
		"output":     map[string]any{"stdout": "/tmp"},
	})
	streamui.ApplyChunk(state.turn.UIState(), map[string]any{
		"type":      "file",
		"url":       "mxc://example.org/out",
		"mediaType": "image/png",
	})

	meta := oc.buildStreamDBMetadata(state)
	if meta == nil {
		t.Fatal("expected metadata")
	}
	if meta.ThinkingContent != "thinking" {
		t.Fatalf("unexpected thinking content: %q", meta.ThinkingContent)
	}
	if len(meta.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %#v", meta.ToolCalls)
	}
	call := meta.ToolCalls[0]
	if call.CallID != "call-1" || call.ToolName != "bash" || call.ToolType != "openclaw" {
		t.Fatalf("unexpected tool call metadata: %#v", call)
	}
	if call.Status != "output-available" || call.ResultStatus != "completed" {
		t.Fatalf("unexpected tool call status: %#v", call)
	}
	if call.Input["cmd"] != "pwd" {
		t.Fatalf("unexpected tool input: %#v", call.Input)
	}
	if call.Output["stdout"] != "/tmp" {
		t.Fatalf("unexpected tool output: %#v", call.Output)
	}
	if len(meta.GeneratedFiles) != 1 {
		t.Fatalf("expected 1 generated file, got %#v", meta.GeneratedFiles)
	}
	if meta.GeneratedFiles[0].URL != "mxc://example.org/out" || meta.GeneratedFiles[0].MimeType != "image/png" {
		t.Fatalf("unexpected generated files: %#v", meta.GeneratedFiles)
	}
}

func TestApplyStreamPartStateLockedUpdatesLifecycleFields(t *testing.T) {
	oc := &OpenClawClient{}
	state := &openClawStreamState{}

	oc.applyStreamPartStateLocked(state, map[string]any{
		"type":      "text-delta",
		"delta":     "hello",
		"timestamp": float64(time.Now().UnixMilli()),
	})
	if got := state.stream.VisibleText(); got != "hello" {
		t.Fatalf("expected visible text to accumulate delta, got %q", got)
	}
	if got := state.stream.AccumulatedText(); got != "hello" {
		t.Fatalf("expected accumulated text to include delta, got %q", got)
	}
	if state.stream.StartedAtMs() == 0 || state.stream.FirstTokenAtMs() == 0 {
		t.Fatalf("expected lifecycle timestamps to be tracked, got started=%d first_token=%d", state.stream.StartedAtMs(), state.stream.FirstTokenAtMs())
	}

	oc.applyStreamPartStateLocked(state, map[string]any{
		"type":      "error",
		"errorText": "boom",
	})
	if state.stream.ErrorText() != "boom" {
		t.Fatalf("expected error text to be captured, got %q", state.stream.ErrorText())
	}
}

func TestBuildStreamDBMetadataFinalizesPreliminaryToolOutput(t *testing.T) {
	turn := newOpenClawTestTurn("turn-tool-seq")
	parts := []map[string]any{
		{
			"type":             "tool-input-available",
			"toolCallId":       "call-2",
			"toolName":         "fetch",
			"input":            map[string]any{"url": "https://example.com"},
			"providerExecuted": true,
		},
		{
			"type":             "tool-output-available",
			"toolCallId":       "call-2",
			"output":           map[string]any{"status": "running"},
			"providerExecuted": true,
			"preliminary":      true,
		},
		{
			"type":             "tool-output-available",
			"toolCallId":       "call-2",
			"output":           map[string]any{"status": 200},
			"providerExecuted": true,
		},
	}
	for _, part := range parts {
		sdk.ApplyStreamPart(turn, part, sdk.PartApplyOptions{})
	}

	oc := &OpenClawClient{}
	state := &openClawStreamState{
		turnID:     "turn-tool-seq",
		agentID:    "main",
		sessionID:  "sess-1",
		sessionKey: "agent:main:matrix-dm",
		role:       "assistant",
		turn:       turn,
	}
	meta := oc.buildStreamDBMetadata(state)
	if meta == nil {
		t.Fatal("expected metadata")
	}
	if len(meta.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %#v", meta.ToolCalls)
	}
	call := meta.ToolCalls[0]
	if call.ToolName != "fetch" || call.CallID != "call-2" {
		t.Fatalf("unexpected tool identity: %#v", call)
	}
	if call.Status != "output-available" || call.ResultStatus != "completed" {
		t.Fatalf("unexpected final tool state: %#v", call)
	}
	if call.Output["status"] != 200 {
		t.Fatalf("unexpected final tool output: %#v", call.Output)
	}
}

func TestDrainAndAbortResetsMap(t *testing.T) {
	// Use states without real turns to avoid nil-cancel panics in unit tests.
	oc := newOpenClawTestClient(map[string]*openClawStreamState{
		"turn-a": {turnID: "turn-a"},
		"turn-b": {turnID: "turn-b"},
	})

	oc.streamHost.DrainAndAbort("disconnect")
	if oc.streamHost.IsActive("turn-a") || oc.streamHost.IsActive("turn-b") {
		t.Fatal("expected stream state map to be cleared after drain")
	}
}

func TestDrainAndAbortHandlesNilCallbacks(t *testing.T) {
	host := sdk.NewStreamTurnHost(sdk.StreamTurnHostCallbacks[openClawStreamState]{})
	host.Lock()
	host.SetLocked("turn-a", &openClawStreamState{turnID: "turn-a"})
	host.Unlock()

	host.DrainAndAbort("disconnect")
	if host.IsActive("turn-a") {
		t.Fatal("expected stream state map to be cleared after drain")
	}
}

func TestEmitStreamPartSerializesTurnCreation(t *testing.T) {
	oc := newOpenClawTestClient(map[string]*openClawStreamState{})
	oc.UserLogin = &bridgev2.UserLogin{Bridge: &bridgev2.Bridge{Bot: testMatrixAPI{}}}
	oc.connector = &OpenClawConnector{}
	oc.connector.sdkConfig = &sdk.Config[*OpenClawClient, *Config]{}

	original := openClawNewSDKStreamTurn
	defer func() { openClawNewSDKStreamTurn = original }()

	var calls int32
	entered := make(chan struct{})
	release := make(chan struct{})
	openClawNewSDKStreamTurn = func(_ *OpenClawClient, _ context.Context, _ *bridgev2.Portal, state *openClawStreamState) *sdk.Turn {
		if atomic.AddInt32(&calls, 1) == 1 {
			close(entered)
			<-release
		}
		return newOpenClawTestTurn(state.turnID)
	}

	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:example.org"}}
	part := map[string]any{"type": "text-delta", "delta": "hello"}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		oc.EmitStreamPart(context.Background(), portal, "turn-race", "agent-1", "session-1", part)
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the first turn creation to start")
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		oc.EmitStreamPart(context.Background(), portal, "turn-race", "agent-1", "session-1", part)
	}()
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected a single turn creation, got %d", got)
	}
}
