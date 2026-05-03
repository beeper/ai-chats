package aihelpers

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func TestApprovalFlow_WaitCancellationDoesNotRemovePending(t *testing.T) {
	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{})
	if _, created := flow.Register("approval-1", time.Minute, &testApprovalFlowData{}); !created {
		t.Fatalf("expected pending approval to be created")
	}

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if decision, ok := flow.Wait(cancelledCtx, "approval-1"); ok || decision != (ApprovalDecisionPayload{}) {
		t.Fatalf("expected cancelled waiter to return zero decision, got %#v ok=%v", decision, ok)
	}
	if flow.Get("approval-1") == nil {
		t.Fatal("expected cancelled waiter to leave pending approval registered")
	}

	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = flow.Resolve("approval-1", ApprovalDecisionPayload{
			ApprovalID: "approval-1",
			Approved:   true,
			Reason:     ApprovalReasonAllowOnce,
		})
	}()

	decision, ok := flow.Wait(context.Background(), "approval-1")
	if !ok {
		t.Fatal("expected another waiter to still receive the decision")
	}
	if !decision.Approved || decision.Reason != ApprovalReasonAllowOnce {
		t.Fatalf("unexpected waiter decision after cancellation: %#v", decision)
	}
}

func TestApprovalFlow_ResolveExternalDoesNotFinalizeWhenAlreadyHandled(t *testing.T) {
	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{
		Login: func() *bridgev2.UserLogin { return nil },
	})
	if _, created := flow.Register("approval-1", time.Minute, &testApprovalFlowData{}); !created {
		t.Fatalf("expected pending approval to be created")
	}

	flow.mu.Lock()
	flow.registerPromptLocked(ApprovalPromptRegistration{
		ApprovalID:      "approval-1",
		PromptMessageID: networkid.MessageID("msg-1"),
		Options:         ApprovalPromptOptions(true),
	})
	flow.mu.Unlock()

	firstDecision := ApprovalDecisionPayload{
		ApprovalID: "approval-1",
		Approved:   true,
		Reason:     "allow_once",
	}
	if err := flow.Resolve("approval-1", firstDecision); err != nil {
		t.Fatalf("expected initial resolve to succeed: %v", err)
	}

	flow.ResolveExternal(context.Background(), "approval-1", ApprovalDecisionPayload{
		ApprovalID: "approval-1",
		Approved:   false,
		Reason:     "deny",
	})

	if flow.Get("approval-1") == nil {
		t.Fatalf("expected duplicate external resolution to keep pending approval intact")
	}
	if _, ok := flow.promptRegistration("approval-1"); !ok {
		t.Fatalf("expected duplicate external resolution to keep prompt registration intact")
	}

	decision, ok := flow.Wait(context.Background(), "approval-1")
	if !ok {
		t.Fatalf("expected waiter to receive the original decision")
	}
	if decision != firstDecision {
		t.Fatalf("expected original decision %#v, got %#v", firstDecision, decision)
	}
}

func TestApprovalFlow_ResolvePreventsLaterTimeout(t *testing.T) {
	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{
		Login: func() *bridgev2.UserLogin { return nil },
	})
	if _, created := flow.Register("approval-1", 25*time.Millisecond, &testApprovalFlowData{}); !created {
		t.Fatalf("expected pending approval to be created")
	}

	flow.mu.Lock()
	flow.registerPromptLocked(ApprovalPromptRegistration{
		ApprovalID:      "approval-1",
		PromptMessageID: networkid.MessageID("msg-1"),
		Options:         ApprovalPromptOptions(true),
		ExpiresAt:       time.Now().Add(25 * time.Millisecond),
	})
	flow.mu.Unlock()

	expected := ApprovalDecisionPayload{
		ApprovalID: "approval-1",
		Approved:   true,
		Reason:     "allow_once",
	}
	if err := flow.Resolve("approval-1", expected); err != nil {
		t.Fatalf("expected resolve to succeed: %v", err)
	}

	time.Sleep(40 * time.Millisecond)

	decision, ok := flow.Wait(context.Background(), "approval-1")
	if !ok {
		t.Fatalf("expected waiter to receive resolved decision after original timeout")
	}
	if decision != expected {
		t.Fatalf("expected decision %#v, got %#v", expected, decision)
	}
}

func TestApprovalFlow_WaitTimeoutFinalizesPromptState(t *testing.T) {
	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{
		Login: func() *bridgev2.UserLogin { return nil },
	})
	if _, created := flow.Register("approval-1", 25*time.Millisecond, &testApprovalFlowData{}); !created {
		t.Fatalf("expected pending approval to be created")
	}

	flow.mu.Lock()
	flow.registerPromptLocked(ApprovalPromptRegistration{
		ApprovalID:      "approval-1",
		PromptMessageID: networkid.MessageID("msg-1"),
		ExpiresAt:       time.Now().Add(25 * time.Millisecond),
		Options:         ApprovalPromptOptions(true),
	})
	flow.mu.Unlock()

	if decision, ok := flow.Wait(context.Background(), "approval-1"); ok || decision != (ApprovalDecisionPayload{}) {
		t.Fatalf("expected wait timeout to return zero decision, got %#v ok=%v", decision, ok)
	}
	if flow.Get("approval-1") != nil {
		t.Fatal("expected wait timeout to finalize pending approval")
	}
	if _, ok := flow.promptRegistration("approval-1"); ok {
		t.Fatal("expected wait timeout to remove prompt registration")
	}
}

func TestApprovalFlow_SchedulePromptTimeoutIgnoresReplacedPrompt(t *testing.T) {
	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{
		Login: func() *bridgev2.UserLogin { return nil },
	})
	if _, created := flow.Register("approval-1", time.Minute, &testApprovalFlowData{}); !created {
		t.Fatalf("expected pending approval to be created")
	}

	firstExpiresAt := time.Now().Add(40 * time.Millisecond)
	flow.mu.Lock()
	flow.registerPromptLocked(ApprovalPromptRegistration{
		ApprovalID: "approval-1",
		ExpiresAt:  firstExpiresAt,
	})
	firstVersion, ok := flow.bindPromptTargetLocked("approval-1", networkid.MessageID("msg-1"))
	flow.mu.Unlock()
	if !ok {
		t.Fatalf("expected initial prompt bind to succeed")
	}
	flow.schedulePromptTimeout("approval-1", firstExpiresAt)

	waitForCondition(t, 50*time.Millisecond, func() bool {
		return flow.Get("approval-1") != nil
	}, "expected pending approval to remain registered before replacement")

	secondExpiresAt := time.Now().Add(160 * time.Millisecond)
	flow.mu.Lock()
	flow.registerPromptLocked(ApprovalPromptRegistration{
		ApprovalID: "approval-1",
		ExpiresAt:  secondExpiresAt,
	})
	secondVersion, ok := flow.bindPromptTargetLocked("approval-1", networkid.MessageID("msg-2"))
	flow.mu.Unlock()
	if !ok {
		t.Fatalf("expected replacement prompt bind to succeed")
	}
	if secondVersion <= firstVersion {
		t.Fatalf("expected replacement prompt version to advance: first=%d second=%d", firstVersion, secondVersion)
	}
	flow.schedulePromptTimeout("approval-1", secondExpiresAt)

	waitForCondition(t, 100*time.Millisecond, func() bool {
		prompt, ok := flow.promptRegistration("approval-1")
		return flow.Get("approval-1") != nil && ok && prompt.PromptMessageID == networkid.MessageID("msg-2")
	}, "expected replacement prompt to remain active after stale timeout window")

	waitForCondition(t, 300*time.Millisecond, func() bool {
		_, ok := flow.promptRegistration("approval-1")
		return flow.Get("approval-1") == nil && !ok
	}, "expected active prompt timeout to finalize pending approval and remove prompt registration")
}

func TestApprovalFlow_SendPromptSendFailureCleansUpRegistration(t *testing.T) {
	a := newTestApprovalActors()
	owner, roomID, portal := a.owner, a.roomID, a.portal
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			UserMXID: owner,
		},
	}

	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{
		Login:    func() *bridgev2.UserLogin { return login },
		IDPrefix: "test",
		LogKey:   "test_msg_id",
	})
	if _, created := flow.Register("approval-1", time.Minute, &testApprovalFlowData{}); !created {
		t.Fatalf("expected pending approval to be created")
	}

	flow.SendPrompt(context.Background(), portal, SendPromptParams{
		ApprovalPromptMessageParams: ApprovalPromptMessageParams{
			ApprovalID:   "approval-1",
			ToolCallID:   "tool-1",
			ToolName:     "exec",
			Presentation: ApprovalPromptPresentation{Title: "Prompt"},
			ExpiresAt:    time.Now().Add(time.Minute),
		},
		RoomID:    roomID,
		OwnerMXID: owner,
	})

	if _, ok := flow.promptRegistration("approval-1"); ok {
		t.Fatalf("expected prompt registration to be cleaned up after send failure")
	}
	if flow.Get("approval-1") == nil {
		t.Fatalf("expected pending approval to remain registered after send failure")
	}
}
