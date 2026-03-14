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

	flow := NewApprovalFlow(ApprovalFlowConfig[*testApprovalFlowData]{
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
	flow.testRedactPromptPlaceholderReacts = func(_ context.Context, _ *bridgev2.UserLogin, _ *bridgev2.Portal, _ bridgev2.EventSender, _ ApprovalPromptRegistration) error {
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
	flow := NewApprovalFlow(ApprovalFlowConfig[*testApprovalFlowData]{
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
	flow := NewApprovalFlow(ApprovalFlowConfig[*testApprovalFlowData]{
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

	flow := NewApprovalFlow(ApprovalFlowConfig[*testApprovalFlowData]{
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
	flow.testRedactPromptPlaceholderReacts = func(_ context.Context, _ *bridgev2.UserLogin, _ *bridgev2.Portal, _ bridgev2.EventSender, _ ApprovalPromptRegistration) error {
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

func TestApprovalFlow_ResolveExternalNotifiesWaiters(t *testing.T) {
	flow := NewApprovalFlow(ApprovalFlowConfig[*testApprovalFlowData]{})
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

func TestApprovalFlow_ResolveExternalDoesNotFinalizeWhenAlreadyHandled(t *testing.T) {
	flow := NewApprovalFlow(ApprovalFlowConfig[*testApprovalFlowData]{
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

func TestApprovalFlow_SchedulePromptTimeoutIgnoresReplacedPrompt(t *testing.T) {
	flow := NewApprovalFlow(ApprovalFlowConfig[*testApprovalFlowData]{
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

	flow := NewApprovalFlow(ApprovalFlowConfig[*testApprovalFlowData]{
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
