package agentremote

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

func TestApprovalFlow_FinishResolvedQueuesEditAndPlaceholderCleanup(t *testing.T) {
	owner := id.UserID("@owner:example.com")
	roomID := id.RoomID("!room:example.com")
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: roomID}}
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			ID:       networkid.UserLoginID("login"),
			UserMXID: owner,
		},
		Bridge: &bridgev2.Bridge{},
	}

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
		PromptEventID:   id.EventID("$prompt"),
		PromptMessageID: networkid.MessageID("msg-1"),
		PromptSenderID:  networkid.UserID("ghost:approval"),
		Options:         DefaultApprovalOptions(),
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
	if isApprovalPlaceholderReaction(&database.Reaction{SenderID: MatrixSenderID(id.UserID("@owner:example.com"))}, prompt, sender) {
		t.Fatalf("did not expect user reaction to be placeholder")
	}
}

func TestApprovalFlow_ReactionRedactionSenderUsesMatrixUser(t *testing.T) {
	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{
		Login: func() *bridgev2.UserLogin {
			return &bridgev2.UserLogin{
				UserLogin: &database.UserLogin{ID: networkid.UserLoginID("login")},
			}
		},
		Sender: func(*bridgev2.Portal) bridgev2.EventSender {
			return bridgev2.EventSender{Sender: networkid.UserID("ghost:approval")}
		},
	})

	sender := flow.reactionRedactionSender(&bridgev2.MatrixReaction{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.ReactionEventContent]{
			Event: &event.Event{Sender: id.UserID("@owner:example.com")},
		},
	})
	if sender.Sender != MatrixSenderID(id.UserID("@owner:example.com")) {
		t.Fatalf("expected matrix sender, got %q", sender.Sender)
	}
	if sender.SenderLogin != networkid.UserLoginID("login") {
		t.Fatalf("expected sender login to be preserved, got %q", sender.SenderLogin)
	}
}

func TestApprovalFlow_HandleReaction_DeliveryErrorKeepsPending(t *testing.T) {
	owner := id.UserID("@owner:example.com")
	roomID := id.RoomID("!room:example.com")
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: roomID}}
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			ID:       networkid.UserLoginID("login"),
			UserMXID: owner,
		},
		Bridge: &bridgev2.Bridge{},
	}

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
		ApprovalID:    "approval-1",
		RoomID:        roomID,
		OwnerMXID:     owner,
		ToolCallID:    "tool-1",
		PromptEventID: id.EventID("$prompt"),
		Options:       DefaultApprovalOptions(),
	})
	flow.mu.Unlock()

	msg := &bridgev2.MatrixReaction{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.ReactionEventContent]{
			Event:  &event.Event{ID: id.EventID("$reaction"), Sender: owner},
			Portal: portal,
		},
	}
	if !flow.HandleReaction(context.Background(), msg, id.EventID("$prompt"), ApprovalReactionKeyAllowOnce) {
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
	owner := id.UserID("@owner:example.com")
	roomID := id.RoomID("!room:example.com")
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: roomID}}
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			ID:       networkid.UserLoginID("login"),
			UserMXID: owner,
		},
		Bridge: &bridgev2.Bridge{},
	}

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
		ApprovalID:    "approval-1",
		RoomID:        roomID,
		OwnerMXID:     owner,
		ToolCallID:    "tool-1",
		PromptEventID: id.EventID("$prompt"),
		Options:       DefaultApprovalOptions(),
	})
	flow.mu.Unlock()

	msg := &bridgev2.MatrixReaction{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.ReactionEventContent]{
			Event:  &event.Event{ID: id.EventID("$reaction"), Sender: owner},
			Portal: portal,
		},
	}
	if !flow.HandleReaction(context.Background(), msg, id.EventID("$prompt"), ApprovalReactionKeyAllowOnce) {
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
	owner := id.UserID("@owner:example.com")
	roomID := id.RoomID("!room:example.com")
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: roomID}}
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			ID:       networkid.UserLoginID("login"),
			UserMXID: owner,
		},
		Bridge: &bridgev2.Bridge{},
	}

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
		PromptEventID:   id.EventID("$prompt"),
		PromptMessageID: networkid.MessageID("msg-1"),
		Options:         DefaultApprovalOptions(),
	}, ApprovalDecisionPayload{
		ApprovalID: "approval-1",
		Approved:   true,
		Reason:     ApprovalReasonAllowOnce,
	})
	flow.mu.Unlock()

	msg := &bridgev2.MatrixReaction{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.ReactionEventContent]{
			Event:  &event.Event{ID: id.EventID("$reaction"), Sender: owner},
			Portal: portal,
		},
	}
	if !flow.HandleReaction(context.Background(), msg, id.EventID("$prompt"), ApprovalReactionKeyDeny) {
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

func TestApprovalFlow_HandleReactionRemove_ResolvedPromptUsesMessageStatus(t *testing.T) {
	owner := id.UserID("@owner:example.com")
	roomID := id.RoomID("!room:example.com")
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: roomID}}
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			ID:       networkid.UserLoginID("login"),
			UserMXID: owner,
		},
		Bridge: &bridgev2.Bridge{},
	}

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
		PromptEventID:   id.EventID("$prompt"),
		PromptMessageID: networkid.MessageID("msg-1"),
		Options:         DefaultApprovalOptions(),
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

func TestApprovalFlow_ResolvedPromptLookupPrunesExpiredEntries(t *testing.T) {
	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{})

	flow.mu.Lock()
	flow.rememberResolvedPromptLocked(ApprovalPromptRegistration{
		ApprovalID:      "approval-1",
		PromptEventID:   id.EventID("$prompt"),
		PromptMessageID: networkid.MessageID("msg-1"),
		ExpiresAt:       time.Now().Add(-time.Second),
		Options:         DefaultApprovalOptions(),
	}, ApprovalDecisionPayload{
		ApprovalID: "approval-1",
		Approved:   true,
		Reason:     ApprovalReasonAllowOnce,
	})
	flow.mu.Unlock()

	if _, ok := flow.resolvedPromptByTarget(id.EventID("$prompt"), ""); ok {
		t.Fatal("expected expired resolved prompt lookup to be pruned")
	}

	flow.mu.Lock()
	defer flow.mu.Unlock()
	if len(flow.resolvedByEventID) != 0 || len(flow.resolvedByMsgID) != 0 {
		t.Fatalf("expected expired resolved prompt entries to be removed, got event=%d msg=%d", len(flow.resolvedByEventID), len(flow.resolvedByMsgID))
	}
}

func TestApprovalFlow_HandleReaction_WrongTargetUniqueApprovalMirrorsDecision(t *testing.T) {
	owner := id.UserID("@owner:example.com")
	roomID := id.RoomID("!room:example.com")
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: roomID}}
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			ID:       networkid.UserLoginID("login"),
			UserMXID: owner,
		},
		Bridge: &bridgev2.Bridge{},
	}

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
		if sender.Sender != MatrixSenderID(owner) {
			t.Errorf("expected mirrored sender to be owner, got %q", sender.Sender)
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
		PromptEventID:   id.EventID("$prompt"),
		PromptMessageID: networkid.MessageID("msg-1"),
		Options:         DefaultApprovalOptions(),
	})
	flow.mu.Unlock()

	msg := &bridgev2.MatrixReaction{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.ReactionEventContent]{
			Event:  &event.Event{ID: id.EventID("$reaction"), Sender: owner},
			Portal: portal,
		},
	}
	if !flow.HandleReaction(context.Background(), msg, id.EventID("$wrong-target"), ApprovalReactionKeyAllowOnce) {
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

func TestApprovalFlow_HandleReaction_WrongTargetAmbiguousApprovalUsesMessageStatus(t *testing.T) {
	owner := id.UserID("@owner:example.com")
	roomID := id.RoomID("!room:example.com")
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: roomID}}
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			ID:       networkid.UserLoginID("login"),
			UserMXID: owner,
		},
		Bridge: &bridgev2.Bridge{},
	}

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
		PromptEventID:   id.EventID("$prompt-1"),
		PromptMessageID: networkid.MessageID("msg-1"),
		Options:         DefaultApprovalOptions(),
	})
	flow.registerPromptLocked(ApprovalPromptRegistration{
		ApprovalID:      "approval-2",
		RoomID:          roomID,
		OwnerMXID:       owner,
		ToolCallID:      "tool-2",
		PromptEventID:   id.EventID("$prompt-2"),
		PromptMessageID: networkid.MessageID("msg-2"),
		Options:         DefaultApprovalOptions(),
	})
	flow.mu.Unlock()

	msg := &bridgev2.MatrixReaction{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.ReactionEventContent]{
			Event:  &event.Event{ID: id.EventID("$reaction"), Sender: owner},
			Portal: portal,
		},
	}
	if !flow.HandleReaction(context.Background(), msg, id.EventID("$wrong-target"), ApprovalReactionKeyAllowOnce) {
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

func TestApprovalFlow_ResolveExternalMirrorsRemoteDecision(t *testing.T) {
	owner := id.UserID("@owner:example.com")
	roomID := id.RoomID("!room:example.com")
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: roomID}}
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			ID:       networkid.UserLoginID("login"),
			UserMXID: owner,
		},
		Bridge: &bridgev2.Bridge{},
	}

	flow := newTestApprovalFlow(t, ApprovalFlowConfig[*testApprovalFlowData]{
		Login: func() *bridgev2.UserLogin { return login },
	})
	flow.testResolvePortal = func(_ context.Context, _ *bridgev2.UserLogin, _ id.RoomID) (*bridgev2.Portal, error) {
		return portal, nil
	}

	mirrorCh := make(chan string, 1)
	flow.testMirrorRemoteDecisionReaction = func(_ context.Context, _ *bridgev2.UserLogin, _ *bridgev2.Portal, sender bridgev2.EventSender, prompt ApprovalPromptRegistration, reactionKey string) {
		if sender.Sender != MatrixSenderID(owner) {
			t.Errorf("expected mirrored reaction sender to be owner, got %q", sender.Sender)
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
		PromptEventID:   id.EventID("$prompt"),
		PromptMessageID: networkid.MessageID("msg-1"),
		Options:         DefaultApprovalOptions(),
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
	owner := id.UserID("@owner:example.com")
	roomID := id.RoomID("!room:example.com")
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: roomID}}
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			ID:       networkid.UserLoginID("login"),
			UserMXID: owner,
		},
		Bridge: &bridgev2.Bridge{},
	}

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
		PromptEventID:   id.EventID("$prompt"),
		PromptMessageID: networkid.MessageID("msg-1"),
		PromptSenderID:  networkid.UserID("ghost:approval"),
		Options:         DefaultApprovalOptions(),
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
		ApprovalID:    "approval-1",
		PromptEventID: id.EventID("$prompt"),
		Options:       DefaultApprovalOptions(),
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
		ApprovalID:    "approval-1",
		PromptEventID: id.EventID("$prompt"),
		Options:       DefaultApprovalOptions(),
		ExpiresAt:     time.Now().Add(25 * time.Millisecond),
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
		ApprovalID:    "approval-1",
		PromptEventID: id.EventID("$prompt"),
		ExpiresAt:     time.Now().Add(25 * time.Millisecond),
		Options:       DefaultApprovalOptions(),
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
	firstVersion, ok := flow.bindPromptIDsLocked("approval-1", id.EventID("$prompt-1"), networkid.MessageID("msg-1"))
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
	secondVersion, ok := flow.bindPromptIDsLocked("approval-1", id.EventID("$prompt-2"), networkid.MessageID("msg-2"))
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
		return flow.Get("approval-1") != nil && ok && prompt.PromptEventID == id.EventID("$prompt-2")
	}, "expected replacement prompt to remain active after stale timeout window")

	waitForCondition(t, 300*time.Millisecond, func() bool {
		_, ok := flow.promptRegistration("approval-1")
		return flow.Get("approval-1") == nil && !ok
	}, "expected active prompt timeout to finalize pending approval and remove prompt registration")
}

func TestApprovalFlow_SendPromptSendFailureCleansUpRegistration(t *testing.T) {
	owner := id.UserID("@owner:example.com")
	roomID := id.RoomID("!room:example.com")
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: roomID}}
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
