package sdk

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func TestApprovalFlow_ResolveExternalMirrorsRemoteDecision(t *testing.T) {
	a := newTestApprovalActors()
	owner, roomID, portal, login := a.owner, a.roomID, a.portal, a.login

	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{
		Login: func() *bridgev2.UserLogin { return login },
	})
	flow.testResolvePortal = func(_ context.Context, _ *bridgev2.UserLogin, _ id.RoomID) (*bridgev2.Portal, error) {
		return portal, nil
	}

	mirrorCh := make(chan string, 1)
	flow.testMirrorRemoteDecisionReaction = func(_ context.Context, _ *bridgev2.UserLogin, _ *bridgev2.Portal, sender bridgev2.EventSender, prompt ApprovalPromptRegistration, reactionKey string) {
		if sender.Sender != "" {
			t.Errorf("expected mirrored reaction sender to be empty, got %q", sender.Sender)
		}
		if prompt.PromptMessageID == "" {
			t.Errorf("expected prompt message id to be set")
		}
		mirrorCh <- reactionKey
	}
	flow.testEditPromptToResolvedState = func(_ context.Context, _ *bridgev2.UserLogin, _ *bridgev2.Portal, _ bridgev2.EventSender, _ ApprovalPromptRegistration, _ ApprovalDecisionPayload) {
	}
	flow.testRedactPromptPlaceholderReacts = func(_ context.Context, _ *bridgev2.UserLogin, _ *bridgev2.Portal, _ bridgev2.EventSender, _ ApprovalPromptRegistration, _ ApprovalPromptReactionCleanupOptions) error {
		return nil
	}

	if _, created := flow.Register("approval-1", time.Minute, &testApprovalFlowData{}); !created {
		t.Fatalf("expected pending approval to be created")
	}
	flow.mu.Lock()
	flow.registerPromptLocked(ApprovalPromptRegistration{
		ApprovalID:      "approval-1",
		RoomID:          roomID,
		OwnerMXID:       owner,
		ToolCallID:      "tool-1",
		PromptMessageID: networkid.MessageID("msg-1"),
		Options:         ApprovalPromptOptions(true),
	})
	flow.mu.Unlock()

	flow.ResolveExternal(context.Background(), "approval-1", ApprovalDecisionPayload{
		ApprovalID: "approval-1",
		Approved:   true,
		Always:     true,
		Reason:     "allow-always",
		ResolvedBy: ApprovalResolutionOriginUser,
	})

	select {
	case key := <-mirrorCh:
		if key != ApprovalReactionKeyAllowAlways {
			t.Fatalf("expected allow_always reaction key, got %q", key)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for mirrored remote reaction")
	}
}

func TestApprovalFlow_ResolveExternalAgentKeepsSelectedPlaceholderReaction(t *testing.T) {
	a := newTestApprovalActors()
	owner, roomID, portal, login := a.owner, a.roomID, a.portal, a.login

	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{
		Login: func() *bridgev2.UserLogin { return login },
	})
	flow.testResolvePortal = func(_ context.Context, _ *bridgev2.UserLogin, _ id.RoomID) (*bridgev2.Portal, error) {
		return portal, nil
	}

	mirrorCalled := make(chan struct{}, 1)
	cleanupCh := make(chan ApprovalPromptReactionCleanupOptions, 1)
	flow.testMirrorRemoteDecisionReaction = func(context.Context, *bridgev2.UserLogin, *bridgev2.Portal, bridgev2.EventSender, ApprovalPromptRegistration, string) {
		mirrorCalled <- struct{}{}
	}
	flow.testEditPromptToResolvedState = func(context.Context, *bridgev2.UserLogin, *bridgev2.Portal, bridgev2.EventSender, ApprovalPromptRegistration, ApprovalDecisionPayload) {
	}
	flow.testRedactPromptPlaceholderReacts = func(_ context.Context, _ *bridgev2.UserLogin, _ *bridgev2.Portal, sender bridgev2.EventSender, _ ApprovalPromptRegistration, opts ApprovalPromptReactionCleanupOptions) error {
		if opts.PreserveSenderID != sender.Sender {
			t.Fatalf("expected preserved sender %q, got %q", sender.Sender, opts.PreserveSenderID)
		}
		cleanupCh <- opts
		return nil
	}

	if _, created := flow.Register("approval-1", time.Minute, &testApprovalFlowData{}); !created {
		t.Fatalf("expected pending approval to be created")
	}
	flow.mu.Lock()
	flow.registerPromptLocked(ApprovalPromptRegistration{
		ApprovalID:      "approval-1",
		RoomID:          roomID,
		OwnerMXID:       owner,
		ToolCallID:      "tool-1",
		PromptMessageID: networkid.MessageID("msg-1"),
		PromptSenderID:  networkid.UserID("ghost:approval"),
		Options:         ApprovalPromptOptions(true),
	})
	flow.mu.Unlock()

	flow.ResolveExternal(context.Background(), "approval-1", ApprovalDecisionPayload{
		ApprovalID: "approval-1",
		Approved:   true,
		Always:     true,
		Reason:     ApprovalReasonAllowAlways,
		ResolvedBy: ApprovalResolutionOriginAgent,
	})

	select {
	case <-mirrorCalled:
		t.Fatalf("did not expect agent-origin decision to mirror a user reaction")
	case <-time.After(50 * time.Millisecond):
	}

	select {
	case opts := <-cleanupCh:
		if opts.PreserveKey != ApprovalReactionKeyAllowAlways {
			t.Fatalf("expected preserved key %q, got %q", ApprovalReactionKeyAllowAlways, opts.PreserveKey)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for placeholder cleanup")
	}
}

func TestApprovalFlow_ResolveExternalNotifiesWaiters(t *testing.T) {
	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{})
	if _, created := flow.Register("approval-1", time.Minute, &testApprovalFlowData{}); !created {
		t.Fatalf("expected pending approval to be created")
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		flow.ResolveExternal(context.Background(), "approval-1", ApprovalDecisionPayload{
			ApprovalID: "approval-1",
			Approved:   true,
			Reason:     "allow_once",
		})
	}()

	waitCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	decision, ok := flow.Wait(waitCtx, "approval-1")
	if !ok {
		t.Fatalf("expected ResolveExternal to notify waiter")
	}
	if !decision.Approved {
		t.Fatalf("expected approved decision, got %#v", decision)
	}
	if decision.Reason != "allow_once" {
		t.Fatalf("expected allow_once reason, got %#v", decision)
	}
}
