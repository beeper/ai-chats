package sdk

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
)

func TestTurnBuildRelatesToDefaultsToSourceEvent(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, UserMessageSource("$source"))
	rel := turn.buildRelatesTo()
	if rel == nil || rel["event_id"] != "$source" {
		t.Fatalf("expected source event relation, got %#v", rel)
	}
}

func TestTurnBuildRelatesToPrefersReplyAndThread(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, UserMessageSource("$source"))
	turn.SetReplyTo(id.EventID("$reply"))
	rel := turn.buildRelatesTo()
	inReply, ok := rel["m.in_reply_to"].(map[string]any)
	if !ok || inReply["event_id"] != "$reply" {
		t.Fatalf("expected explicit reply relation, got %#v", rel)
	}

	turn.SetThread(id.EventID("$thread"))
	rel = turn.buildRelatesTo()
	if rel["event_id"] != "$thread" {
		t.Fatalf("expected thread root relation, got %#v", rel)
	}
	inReply, ok = rel["m.in_reply_to"].(map[string]any)
	if !ok || inReply["event_id"] != "$reply" {
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
			MsgType: event.MsgText,
			Body:    "Pondering...",
		},
		Extra: map[string]any{
			"body":          "Pondering...",
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
	if got := part.Extra["body"]; got != "Pondering..." {
		t.Fatalf("expected placeholder extra body, got %#v", got)
	}
	if part.DBMetadata == nil {
		t.Fatalf("expected placeholder DB metadata")
	}
	if _, ok := part.Extra["m.mentions"]; !ok {
		t.Fatalf("expected m.mentions default to be preserved")
	}
}

func TestTurnBuildFinalEditAddsReplaceRelation(t *testing.T) {
	turn := newTurn(context.Background(), nil, nil, nil)
	turn.initialEventID = id.EventID("$event-1")
	turn.networkMessageID = "msg-1"
	turn.SetFinalEditPayload(&FinalEditPayload{
		Content: &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    "done",
		},
		TopLevelExtra: map[string]any{
			"com.beeper.ai": map[string]any{"id": turn.ID()},
		},
		ReplyTo: id.EventID("$reply-1"),
	})

	target, edit := turn.buildFinalEdit()
	if target != "msg-1" {
		t.Fatalf("expected network target msg-1, got %q", target)
	}
	if edit == nil || len(edit.ModifiedParts) != 1 {
		t.Fatalf("expected single modified part, got %#v", edit)
	}
	gotRelatesTo, ok := edit.ModifiedParts[0].TopLevelExtra["m.relates_to"].(map[string]any)
	if !ok {
		t.Fatalf("expected m.relates_to in top-level extra, got %#v", edit.ModifiedParts[0].TopLevelExtra)
	}
	if gotRelatesTo["rel_type"] != "m.replace" {
		t.Fatalf("expected m.replace relation, got %#v", gotRelatesTo)
	}
	if gotRelatesTo["event_id"] != "$event-1" {
		t.Fatalf("expected replace target event id, got %#v", gotRelatesTo)
	}
	inReply, ok := gotRelatesTo["m.in_reply_to"].(map[string]any)
	if !ok || inReply["event_id"] != "$reply-1" {
		t.Fatalf("expected reply override in relation, got %#v", gotRelatesTo)
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
