package sdk

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestApprovalFlow_HandleReaction_ResolvedPromptUsesEventIDWhenMessageIDMissing(t *testing.T) {
	a := newTestApprovalActors()
	owner, roomID, portal, login := a.owner, a.roomID, a.portal, a.login

	var redacted bool
	var status bridgev2.MessageStatus
	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{
		Login: func() *bridgev2.UserLogin { return login },
	})
	flow.testRedactSingleReaction = func(_ *bridgev2.MatrixReaction) {
		redacted = true
	}
	flow.testSendMessageStatus = func(_ context.Context, gotPortal *bridgev2.Portal, evt *event.Event, gotStatus bridgev2.MessageStatus) {
		if gotPortal != portal {
			t.Fatalf("expected status portal %p, got %p", portal, gotPortal)
		}
		if evt == nil || evt.ID != id.EventID("$reaction") {
			t.Fatalf("expected reaction event status target, got %#v", evt)
		}
		status = gotStatus
	}
	flow.mu.Lock()
	flow.rememberResolvedPromptLocked(ApprovalPromptRegistration{
		ApprovalID:      "approval-1",
		RoomID:          roomID,
		OwnerMXID:       owner,
		PromptMessageID: networkid.MessageID("$prompt"),
		Options:         ApprovalPromptOptions(true),
	}, ApprovalDecisionPayload{
		ApprovalID: "approval-1",
		Approved:   true,
		Reason:     ApprovalReasonAllowOnce,
	})
	flow.mu.Unlock()

	msg := testMatrixReaction(portal, owner, id.EventID("$reaction"), id.EventID("$prompt"), "", ApprovalReactionKeyDeny)
	if !flow.HandleReaction(context.Background(), msg) {
		t.Fatalf("expected resolved approval reaction to be handled")
	}
	if !redacted {
		t.Fatalf("expected late approval reaction to be redacted")
	}
	if status.Status != event.MessageStatusFail {
		t.Fatalf("expected fail status, got %#v", status)
	}
	if status.ErrorReason != event.MessageStatusGenericError {
		t.Fatalf("expected generic error reason, got %#v", status)
	}
	if status.Message != approvalResolvedMSSMessage {
		t.Fatalf("expected resolved approval status message, got %q", status.Message)
	}
}

func TestApprovalFlow_HandleReactionRemove_ResolvedPromptUsesMessageStatusForAlias(t *testing.T) {
	a := newTestApprovalActors()
	owner, roomID, portal, login := a.owner, a.roomID, a.portal, a.login

	var status bridgev2.MessageStatus
	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{
		Login: func() *bridgev2.UserLogin { return login },
	})
	flow.testSendMessageStatus = func(_ context.Context, gotPortal *bridgev2.Portal, evt *event.Event, gotStatus bridgev2.MessageStatus) {
		if gotPortal != portal {
			t.Fatalf("expected status portal %p, got %p", portal, gotPortal)
		}
		if evt == nil || evt.ID != id.EventID("$redaction") {
			t.Fatalf("expected redaction event status target, got %#v", evt)
		}
		status = gotStatus
	}
	flow.mu.Lock()
	flow.rememberResolvedPromptLocked(ApprovalPromptRegistration{
		ApprovalID:      "approval-1",
		RoomID:          roomID,
		OwnerMXID:       owner,
		PromptMessageID: networkid.MessageID("msg-1"),
		Options:         ApprovalPromptOptions(true),
	}, ApprovalDecisionPayload{
		ApprovalID: "approval-1",
		Approved:   true,
		Reason:     ApprovalReasonAllowOnce,
	})
	flow.mu.Unlock()

	handled := flow.HandleReactionRemove(context.Background(), &bridgev2.MatrixReactionRemove{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.RedactionEventContent]{
			Event:  &event.Event{ID: id.EventID("$redaction"), Sender: owner},
			Portal: portal,
		},
		TargetReaction: &database.Reaction{
			MessageID: networkid.MessageID("msg-1"),
			Emoji:     ApprovalReactionAliasAllowOnce,
		},
	})
	if !handled {
		t.Fatalf("expected resolved alias approval reaction removal to be handled")
	}
	if status.Status != event.MessageStatusFail {
		t.Fatalf("expected fail status, got %#v", status)
	}
	if status.ErrorReason != event.MessageStatusGenericError {
		t.Fatalf("expected generic error reason, got %#v", status)
	}
	if status.Message != approvalResolvedMSSMessage {
		t.Fatalf("expected resolved approval status message, got %q", status.Message)
	}
}

func TestApprovalPlaceholderReactionKey_PrefersAlias(t *testing.T) {
	if got := approvalPlaceholderReactionKey(ApprovalOption{
		Key:         ApprovalReactionKeyAllowOnce,
		FallbackKey: ApprovalReactionAliasAllowOnce,
	}); got != ApprovalReactionAliasAllowOnce {
		t.Fatalf("expected placeholder alias %q, got %q", ApprovalReactionAliasAllowOnce, got)
	}

	if got := approvalPlaceholderReactionKey(ApprovalOption{
		Key: ApprovalReactionKeyDeny,
	}); got != ApprovalReactionKeyDeny {
		t.Fatalf("expected canonical placeholder key %q, got %q", ApprovalReactionKeyDeny, got)
	}
}

func TestApprovalOptionKeyForDecision_FallsBackToFallbackKey(t *testing.T) {
	options := []ApprovalOption{
		{
			ID:          ApprovalReasonAllowOnce,
			FallbackKey: ApprovalReactionAliasAllowOnce,
			Approved:    true,
			Reason:      ApprovalReasonAllowOnce,
		},
		{
			ID:          ApprovalReasonAllowAlways,
			FallbackKey: ApprovalReactionAliasAllowAlways,
			Approved:    true,
			Always:      true,
			Reason:      ApprovalReasonAllowAlways,
		},
		{
			ID:          ApprovalReasonDeny,
			FallbackKey: ApprovalReactionAliasDeny,
			Approved:    false,
			Reason:      ApprovalReasonDeny,
		},
	}

	for _, tc := range []struct {
		name     string
		decision ApprovalDecisionPayload
		want     string
	}{
		{
			name: "approved once",
			decision: ApprovalDecisionPayload{
				Approved: true,
				Reason:   ApprovalReasonAllowOnce,
			},
			want: normalizeReactionKey(ApprovalReactionAliasAllowOnce),
		},
		{
			name: "approved always",
			decision: ApprovalDecisionPayload{
				Approved: true,
				Always:   true,
				Reason:   ApprovalReasonAllowAlways,
			},
			want: normalizeReactionKey(ApprovalReactionAliasAllowAlways),
		},
		{
			name: "denied",
			decision: ApprovalDecisionPayload{
				Approved: false,
				Reason:   ApprovalReasonDeny,
			},
			want: normalizeReactionKey(ApprovalReactionAliasDeny),
		},
		{
			name: "timeout remains empty",
			decision: ApprovalDecisionPayload{
				Approved: false,
				Reason:   ApprovalReasonTimeout,
			},
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := approvalOptionKeyForDecision(options, tc.decision); got != tc.want {
				t.Fatalf("expected decision key %q, got %q", tc.want, got)
			}
		})
	}
}

func TestApprovalReactionTargetMessageID_PrefersAssistantTurnTarget(t *testing.T) {
	prompt := ApprovalPromptRegistration{
		ReactionTargetMessageID: networkid.MessageID("assistant-msg"),
		PromptMessageID:         networkid.MessageID("prompt-msg"),
	}
	if got := approvalReactionTargetMessageID(prompt); got != networkid.MessageID("assistant-msg") {
		t.Fatalf("expected assistant-turn reaction target, got %q", got)
	}

	prompt.ReactionTargetMessageID = ""
	if got := approvalReactionTargetMessageID(prompt); got != networkid.MessageID("prompt-msg") {
		t.Fatalf("expected prompt fallback reaction target, got %q", got)
	}
}

func TestApprovalFlow_ResolvedPromptLookupPrunesExpiredEntries(t *testing.T) {
	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{})

	flow.mu.Lock()
	flow.rememberResolvedPromptLocked(ApprovalPromptRegistration{
		ApprovalID:      "approval-1",
		PromptMessageID: networkid.MessageID("msg-1"),
		ExpiresAt:       time.Now().Add(-time.Second),
		Options:         ApprovalPromptOptions(true),
	}, ApprovalDecisionPayload{
		ApprovalID: "approval-1",
		Approved:   true,
		Reason:     ApprovalReasonAllowOnce,
	})
	flow.mu.Unlock()

	if _, ok := flow.resolvedPromptByTarget(networkid.MessageID("msg-1")); ok {
		t.Fatal("expected expired resolved prompt lookup to be pruned")
	}

	flow.mu.Lock()
	defer flow.mu.Unlock()
	if len(flow.resolvedByMsgID) != 0 {
		t.Fatalf("expected expired resolved prompt entries to be removed, got msg=%d", len(flow.resolvedByMsgID))
	}
}

func TestApprovalFlow_HandleReaction_WrongTargetUniqueApprovalMirrorsDecision(t *testing.T) {
	a := newTestApprovalActors()
	owner, roomID, portal, login := a.owner, a.roomID, a.portal, a.login

	var redacted bool
	mirrorCh := make(chan string, 1)
	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{
		Login: func() *bridgev2.UserLogin { return login },
	})
	flow.testResolvePortal = func(_ context.Context, _ *bridgev2.UserLogin, _ id.RoomID) (*bridgev2.Portal, error) {
		return portal, nil
	}
	flow.testRedactSingleReaction = func(_ *bridgev2.MatrixReaction) {
		redacted = true
	}
	flow.testMirrorRemoteDecisionReaction = func(_ context.Context, _ *bridgev2.UserLogin, _ *bridgev2.Portal, sender bridgev2.EventSender, prompt ApprovalPromptRegistration, reactionKey string) {
		if sender.Sender != "" {
			t.Errorf("expected mirrored sender to be empty, got %q", sender.Sender)
		}
		if prompt.PromptMessageID != networkid.MessageID("msg-1") {
			t.Errorf("expected prompt message id msg-1, got %q", prompt.PromptMessageID)
		}
		mirrorCh <- reactionKey
	}
	flow.testEditPromptToResolvedState = func(context.Context, *bridgev2.UserLogin, *bridgev2.Portal, bridgev2.EventSender, ApprovalPromptRegistration, ApprovalDecisionPayload) {
	}
	flow.testRedactPromptPlaceholderReacts = func(context.Context, *bridgev2.UserLogin, *bridgev2.Portal, bridgev2.EventSender, ApprovalPromptRegistration, ApprovalPromptReactionCleanupOptions) error {
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

	msg := testMatrixReaction(portal, owner, id.EventID("$reaction"), id.EventID("$wrong-target"), "", ApprovalReactionKeyAllowOnce)
	if !flow.HandleReaction(context.Background(), msg) {
		t.Fatalf("expected wrong-target approval reaction to be handled")
	}
	if flow.Get("approval-1") != nil {
		t.Fatalf("expected pending approval to be finalized")
	}
	if !redacted {
		t.Fatalf("expected wrong-target reaction to be redacted")
	}

	select {
	case key := <-mirrorCh:
		if key != ApprovalReactionKeyAllowOnce {
			t.Fatalf("expected mirrored allow-once reaction, got %q", key)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for mirrored approval reaction")
	}
}

func TestApprovalFlow_HandleReaction_WrongTargetUniqueApprovalPreservesAliasReaction(t *testing.T) {
	a := newTestApprovalActors()
	owner, roomID, portal, login := a.owner, a.roomID, a.portal, a.login

	var redacted bool
	mirrorCh := make(chan string, 1)
	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{
		Login: func() *bridgev2.UserLogin { return login },
	})
	flow.testResolvePortal = func(_ context.Context, _ *bridgev2.UserLogin, _ id.RoomID) (*bridgev2.Portal, error) {
		return portal, nil
	}
	flow.testRedactSingleReaction = func(_ *bridgev2.MatrixReaction) {
		redacted = true
	}
	flow.testMirrorRemoteDecisionReaction = func(_ context.Context, _ *bridgev2.UserLogin, _ *bridgev2.Portal, _ bridgev2.EventSender, _ ApprovalPromptRegistration, reactionKey string) {
		mirrorCh <- reactionKey
	}
	flow.testEditPromptToResolvedState = func(context.Context, *bridgev2.UserLogin, *bridgev2.Portal, bridgev2.EventSender, ApprovalPromptRegistration, ApprovalDecisionPayload) {
	}
	flow.testRedactPromptPlaceholderReacts = func(context.Context, *bridgev2.UserLogin, *bridgev2.Portal, bridgev2.EventSender, ApprovalPromptRegistration, ApprovalPromptReactionCleanupOptions) error {
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

	msg := testMatrixReaction(portal, owner, id.EventID("$reaction"), id.EventID("$wrong-target"), "", ApprovalReactionAliasAllowOnce)
	if !flow.HandleReaction(context.Background(), msg) {
		t.Fatalf("expected wrong-target alias approval reaction to be handled")
	}
	if !redacted {
		t.Fatalf("expected wrong-target alias reaction to be redacted")
	}

	select {
	case key := <-mirrorCh:
		if key != ApprovalReactionAliasAllowOnce {
			t.Fatalf("expected mirrored allow-once alias, got %q", key)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for mirrored alias approval reaction")
	}
}

func TestApprovalFlow_HandleReaction_WrongTargetAmbiguousApprovalUsesMessageStatus(t *testing.T) {
	a := newTestApprovalActors()
	owner, roomID, portal, login := a.owner, a.roomID, a.portal, a.login

	var redacted bool
	var (
		statusEvt *event.Event
		status    bridgev2.MessageStatus
	)
	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{
		Login: func() *bridgev2.UserLogin { return login },
	})
	flow.testRedactSingleReaction = func(_ *bridgev2.MatrixReaction) {
		redacted = true
	}
	flow.testSendMessageStatus = func(_ context.Context, gotPortal *bridgev2.Portal, evt *event.Event, gotStatus bridgev2.MessageStatus) {
		if gotPortal != portal {
			t.Fatalf("expected status to target original portal")
		}
		statusEvt = evt
		status = gotStatus
	}

	for _, approvalID := range []string{"approval-1", "approval-2"} {
		if _, created := flow.Register(approvalID, time.Minute, &testApprovalFlowData{}); !created {
			t.Fatalf("expected pending approval %s to be created", approvalID)
		}
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
	flow.registerPromptLocked(ApprovalPromptRegistration{
		ApprovalID:      "approval-2",
		RoomID:          roomID,
		OwnerMXID:       owner,
		ToolCallID:      "tool-2",
		PromptMessageID: networkid.MessageID("msg-2"),
		Options:         ApprovalPromptOptions(true),
	})
	flow.mu.Unlock()

	msg := testMatrixReaction(portal, owner, id.EventID("$reaction"), id.EventID("$wrong-target"), "", ApprovalReactionKeyAllowOnce)
	if !flow.HandleReaction(context.Background(), msg) {
		t.Fatalf("expected ambiguous wrong-target approval reaction to be handled")
	}
	if !redacted {
		t.Fatalf("expected ambiguous wrong-target reaction to be redacted")
	}
	if statusEvt == nil {
		t.Fatalf("expected message status to be sent")
	}
	if statusEvt.ID != id.EventID("$reaction") {
		t.Fatalf("expected message status for reaction event, got %q", statusEvt.ID)
	}
	if status.Status != event.MessageStatusFail {
		t.Fatalf("expected failed message status, got %q", status.Status)
	}
	if status.ErrorReason != event.MessageStatusGenericError {
		t.Fatalf("expected generic error reason, got %q", status.ErrorReason)
	}
	if status.Message != approvalWrongTargetMSSMessage {
		t.Fatalf("unexpected message status text: %q", status.Message)
	}
	if !status.IsCertain {
		t.Fatalf("expected message status to be certain")
	}
	if status.SendNotice {
		t.Fatalf("did not expect message status to request a notice")
	}
	if flow.Get("approval-1") == nil || flow.Get("approval-2") == nil {
		t.Fatalf("expected ambiguous approvals to remain pending")
	}
}
