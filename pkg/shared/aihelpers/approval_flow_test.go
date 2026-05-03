package aihelpers

import (
	"context"
	"errors"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type testApprovalFlowData struct{}

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("%s", message)
	}
}

func newTestApprovalFlow(t *testing.T, cfg ApprovalFlowConfig[*testApprovalFlowData]) *ApprovalFlow[*testApprovalFlowData] {
	t.Helper()
	flow := NewApprovalFlow(cfg)
	t.Cleanup(flow.Close)
	return flow
}

func testMatrixReaction(
	portal *bridgev2.Portal,
	sender id.UserID,
	reactionID id.EventID,
	targetEventID id.EventID,
	targetMessageID networkid.MessageID,
	key string,
) *bridgev2.MatrixReaction {
	return &bridgev2.MatrixReaction{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.ReactionEventContent]{
			Event: &event.Event{
				ID:     reactionID,
				Sender: sender,
			},
			Content: &event.ReactionEventContent{
				RelatesTo: event.RelatesTo{
					Type:    event.RelAnnotation,
					EventID: targetEventID,
					Key:     key,
				},
			},
			Portal: portal,
		},
		TargetMessage: &database.Message{
			ID:   targetMessageID,
			MXID: targetEventID,
		},
	}
}

type testApprovalActors struct {
	owner  id.UserID
	roomID id.RoomID
	portal *bridgev2.Portal
	login  *bridgev2.UserLogin
}

func newTestApprovalActors() testApprovalActors {
	owner := id.UserID("@owner:example.com")
	roomID := id.RoomID("!room:example.com")
	return testApprovalActors{
		owner:  owner,
		roomID: roomID,
		portal: &bridgev2.Portal{Portal: &database.Portal{MXID: roomID}},
		login: &bridgev2.UserLogin{
			UserLogin: &database.UserLogin{
				ID:       networkid.UserLoginID("login"),
				UserMXID: owner,
			},
			Bridge: &bridgev2.Bridge{},
		},
	}
}

func TestApprovalFlow_FinishResolvedQueuesEditAndPlaceholderCleanup(t *testing.T) {
	a := newTestApprovalActors()
	owner, roomID, portal, login := a.owner, a.roomID, a.portal, a.login

	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{
		Login: func() *bridgev2.UserLogin { return login },
	})
	flow.testResolvePortal = func(_ context.Context, _ *bridgev2.UserLogin, _ id.RoomID) (*bridgev2.Portal, error) {
		return portal, nil
	}

	editCh := make(chan ApprovalDecisionPayload, 1)
	cleanupCh := make(chan struct{}, 1)
	flow.testEditPromptToResolvedState = func(_ context.Context, _ *bridgev2.UserLogin, _ *bridgev2.Portal, _ bridgev2.EventSender, prompt ApprovalPromptRegistration, decision ApprovalDecisionPayload) {
		if prompt.PromptMessageID == "" {
			t.Errorf("expected prompt message id to be set")
		}
		editCh <- decision
	}
	flow.testRedactPromptPlaceholderReacts = func(_ context.Context, _ *bridgev2.UserLogin, _ *bridgev2.Portal, _ bridgev2.EventSender, _ ApprovalPromptRegistration, _ ApprovalPromptReactionCleanupOptions) error {
		cleanupCh <- struct{}{}
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
		ToolName:        "exec",
		PromptMessageID: networkid.MessageID("msg-1"),
		PromptSenderID:  networkid.UserID("ghost:approval"),
		Options:         ApprovalPromptOptions(true),
	})
	flow.mu.Unlock()

	flow.FinishResolved("approval-1", ApprovalDecisionPayload{
		ApprovalID: "approval-1",
		Approved:   true,
		Reason:     "allow_once",
	})
	if pending := flow.Get("approval-1"); pending != nil {
		t.Fatalf("expected pending approval to be finalized")
	}
	flow.mu.Lock()
	_, stillPrompt := flow.promptsByApproval["approval-1"]
	flow.mu.Unlock()
	if stillPrompt {
		t.Fatalf("expected prompt registration to be finalized")
	}

	select {
	case <-cleanupCh:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for placeholder cleanup scheduling")
	}

	select {
	case decision := <-editCh:
		if !decision.Approved {
			t.Fatalf("expected approved decision, got %#v", decision)
		}
		if decision.Reason != "allow_once" {
			t.Fatalf("expected reason allow_once, got %#v", decision)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for prompt edit scheduling")
	}
}

func TestIsApprovalPlaceholderReaction_ExcludesUserReaction(t *testing.T) {
	prompt := ApprovalPromptRegistration{
		PromptSenderID: networkid.UserID("ghost:approval"),
	}
	sender := bridgev2.EventSender{Sender: networkid.UserID("ghost:approval")}

	if !isApprovalPlaceholderReaction(&database.Reaction{SenderID: networkid.UserID("ghost:approval")}, prompt, sender) {
		t.Fatalf("expected bridge-authored reaction to be placeholder")
	}
	if isApprovalPlaceholderReaction(&database.Reaction{SenderID: networkid.UserID("mxid:@owner:example.com")}, prompt, sender) {
		t.Fatalf("did not expect user reaction to be placeholder")
	}
}

func TestApprovalFlow_HandleReaction_DeliveryErrorKeepsPending(t *testing.T) {
	a := newTestApprovalActors()
	owner, roomID, portal, login := a.owner, a.roomID, a.portal, a.login

	var redacted bool
	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{
		Login: func() *bridgev2.UserLogin { return login },
		DeliverDecision: func(_ context.Context, _ *bridgev2.Portal, _ *Pending[*testApprovalFlowData], _ ApprovalDecisionPayload) error {
			return errors.New("boom")
		},
	})
	flow.testRedactSingleReaction = func(_ *bridgev2.MatrixReaction) {
		redacted = true
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

	msg := testMatrixReaction(portal, owner, id.EventID("$reaction"), id.EventID("$prompt"), networkid.MessageID("msg-1"), ApprovalReactionKeyAllowOnce)
	if !flow.HandleReaction(context.Background(), msg) {
		t.Fatalf("expected approval reaction to be handled")
	}
	if flow.Get("approval-1") == nil {
		t.Fatalf("expected pending approval to remain after delivery error")
	}
	if !redacted {
		t.Fatalf("expected failed user reaction to be redacted")
	}
}

func TestApprovalFlow_HandleReaction_UnknownPendingShowsUnknown(t *testing.T) {
	a := newTestApprovalActors()
	owner, roomID, portal, login := a.owner, a.roomID, a.portal, a.login

	var redacted bool
	var notice string
	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{
		Login: func() *bridgev2.UserLogin { return login },
		SendNotice: func(_ context.Context, _ *bridgev2.Portal, msg string) {
			notice = msg
		},
		DeliverDecision: func(_ context.Context, _ *bridgev2.Portal, _ *Pending[*testApprovalFlowData], _ ApprovalDecisionPayload) error {
			t.Fatal("did not expect DeliverDecision to be called")
			return nil
		},
	})
	flow.testRedactSingleReaction = func(_ *bridgev2.MatrixReaction) {
		redacted = true
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

	msg := testMatrixReaction(portal, owner, id.EventID("$reaction"), id.EventID("$prompt"), networkid.MessageID("msg-1"), ApprovalReactionKeyAllowOnce)
	if !flow.HandleReaction(context.Background(), msg) {
		t.Fatalf("expected approval reaction to be handled")
	}
	if !redacted {
		t.Fatalf("expected unknown approval reaction to be redacted")
	}
	if notice == "" {
		t.Fatalf("expected unknown approval notice")
	}
}

func TestApprovalFlow_HandleReaction_ResolvedPromptUsesMessageStatus(t *testing.T) {
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
		PromptMessageID: networkid.MessageID("msg-1"),
		Options:         ApprovalPromptOptions(true),
	}, ApprovalDecisionPayload{
		ApprovalID: "approval-1",
		Approved:   true,
		Reason:     ApprovalReasonAllowOnce,
	})
	flow.mu.Unlock()

	msg := testMatrixReaction(portal, owner, id.EventID("$reaction"), id.EventID("$prompt"), networkid.MessageID("msg-1"), ApprovalReactionKeyDeny)
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

func TestApprovalFlow_HandleReaction_MatchesPromptByMessageID(t *testing.T) {
	a := newTestApprovalActors()
	owner, roomID, portal := a.owner, a.roomID, a.portal

	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{})
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

	msg := testMatrixReaction(portal, owner, id.EventID("$reaction"), "", networkid.MessageID("msg-1"), ApprovalReactionKeyAllowOnce)
	if !flow.HandleReaction(context.Background(), msg) {
		t.Fatalf("expected approval reaction to be handled")
	}
	if pending := flow.Get("approval-1"); pending != nil {
		t.Fatalf("expected pending approval to be finalized")
	}
}

func TestApprovalFlow_HandleReaction_MatchesPromptByEventIDWhenMessageIDMissing(t *testing.T) {
	a := newTestApprovalActors()
	owner, roomID, portal := a.owner, a.roomID, a.portal

	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{})
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
		PromptMessageID: networkid.MessageID("$prompt-1"),
		Options:         ApprovalPromptOptions(true),
	})
	flow.registerPromptLocked(ApprovalPromptRegistration{
		ApprovalID:      "approval-2",
		RoomID:          roomID,
		OwnerMXID:       owner,
		ToolCallID:      "tool-2",
		PromptMessageID: networkid.MessageID("$prompt-2"),
		Options:         ApprovalPromptOptions(true),
	})
	flow.mu.Unlock()

	msg := testMatrixReaction(portal, owner, id.EventID("$reaction"), id.EventID("$prompt-1"), "", ApprovalReactionKeyAllowOnce)
	if !flow.HandleReaction(context.Background(), msg) {
		t.Fatalf("expected approval reaction to be handled")
	}
	if pending := flow.Get("approval-1"); pending != nil {
		t.Fatalf("expected targeted approval to be finalized")
	}
	if pending := flow.Get("approval-2"); pending == nil {
		t.Fatalf("expected non-targeted approval to remain pending")
	}
}

func TestApprovalFlow_HandleReactionRemove_ResolvedPromptUsesMessageStatus(t *testing.T) {
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
			Emoji:     ApprovalReactionKeyAllowOnce,
		},
	})
	if !handled {
		t.Fatalf("expected resolved approval reaction removal to be handled")
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
