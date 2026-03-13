package sdk

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
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
	turn.SetMetadata(map[string]any{
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
	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			MXID: "!room:test",
		},
	}
	turn := newTurn(context.Background(), newConversation(context.Background(), portal, login, bridgev2.EventSender{}, runtime), nil, nil)

	handle := turn.RequestApproval(ApprovalRequest{
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
	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			MXID: "!room:test",
		},
	}
	turn := newTurn(context.Background(), newConversation(context.Background(), portal, login, bridgev2.EventSender{}, runtime), nil, nil)

	handle := turn.RequestApproval(ApprovalRequest{
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
