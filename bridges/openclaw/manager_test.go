package openclaw

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote"
)

func TestShouldMirrorLatestUserMessageFromHistory(t *testing.T) {
	now := time.Date(2026, time.March, 11, 13, 22, 59, 0, time.UTC)

	t.Run("rejects beeper originated matrix events", func(t *testing.T) {
		payload := gatewayChatEvent{
			RunID: "run-web-1",
			TS:    now.UnixMilli(),
			Message: map[string]any{
				"role":      "assistant",
				"timestamp": now.UnixMilli(),
			},
		}
		message := map[string]any{
			"role":           "user",
			"timestamp":      now.Add(-2 * time.Second).UnixMilli(),
			"idempotencyKey": "$eventid:beeper.local",
		}
		if shouldMirrorLatestUserMessageFromHistory(payload, message) {
			t.Fatal("expected Matrix-originated user message to be skipped")
		}
	})

	t.Run("accepts matching webchat run id", func(t *testing.T) {
		payload := gatewayChatEvent{
			RunID: "run-web-2",
			TS:    now.UnixMilli(),
			Message: map[string]any{
				"role":      "assistant",
				"timestamp": now.UnixMilli(),
			},
		}
		message := map[string]any{
			"role":           "user",
			"timestamp":      now.Add(-3 * time.Second).UnixMilli(),
			"idempotencyKey": "run-web-2",
		}
		if !shouldMirrorLatestUserMessageFromHistory(payload, message) {
			t.Fatal("expected matching webchat user message to be mirrored")
		}
	})

	t.Run("rejects mismatched run markers", func(t *testing.T) {
		payload := gatewayChatEvent{
			RunID: "run-web-3",
			TS:    now.UnixMilli(),
			Message: map[string]any{
				"role":      "assistant",
				"timestamp": now.UnixMilli(),
			},
		}
		message := map[string]any{
			"role":           "user",
			"timestamp":      now.Add(-3 * time.Second).UnixMilli(),
			"idempotencyKey": "different-run",
		}
		if shouldMirrorLatestUserMessageFromHistory(payload, message) {
			t.Fatal("expected mismatched run markers to be skipped")
		}
	})

	t.Run("falls back to recent markerless messages only", func(t *testing.T) {
		payload := gatewayChatEvent{
			RunID: "run-web-4",
			TS:    now.UnixMilli(),
			Message: map[string]any{
				"role":      "assistant",
				"timestamp": now.UnixMilli(),
			},
		}
		recent := map[string]any{
			"role":      "user",
			"timestamp": now.Add(-2 * time.Minute).UnixMilli(),
		}
		if !shouldMirrorLatestUserMessageFromHistory(payload, recent) {
			t.Fatal("expected recent markerless user message to be mirrored as fallback")
		}

		stale := map[string]any{
			"role":      "user",
			"timestamp": now.Add(-(openClawHistoryMirrorFallbackWindow + time.Minute)).UnixMilli(),
		}
		if shouldMirrorLatestUserMessageFromHistory(payload, stale) {
			t.Fatal("expected stale markerless user message to be skipped")
		}
	})
}

func TestOpenClawRemoteMessageGetStreamOrderUsesGatewaySeq(t *testing.T) {
	ts := time.Date(2026, time.March, 12, 12, 0, 0, 0, time.UTC)
	first := buildOpenClawRemoteMessage(networkid.PortalKey{}, "first", bridgev2.EventSender{}, ts, 10, nil)
	second := buildOpenClawRemoteMessage(networkid.PortalKey{}, "second", bridgev2.EventSender{}, ts, 11, nil)
	if first.GetStreamOrder() != 10 {
		t.Fatalf("expected first stream order 10, got %d", first.GetStreamOrder())
	}
	if second.GetStreamOrder() != 11 {
		t.Fatalf("expected second stream order 11, got %d", second.GetStreamOrder())
	}
	if second.GetStreamOrder() <= first.GetStreamOrder() {
		t.Fatalf("expected gateway seq ordering to be strictly increasing")
	}
}

func TestPrepareOpenClawBackfillEntriesUsesTranscriptSequence(t *testing.T) {
	meta := &PortalMetadata{OpenClawSessionKey: "agent:main:test"}
	entries := prepareOpenClawBackfillEntries(meta, []map[string]any{
		{
			"role":      "assistant",
			"text":      "second",
			"timestamp": time.Date(2026, time.March, 12, 12, 0, 3, 0, time.UTC).UnixMilli(),
			"__openclaw": map[string]any{
				"seq": 11,
			},
		},
		{
			"role":      "assistant",
			"text":      "first",
			"timestamp": time.Date(2026, time.March, 12, 12, 0, 9, 0, time.UTC).UnixMilli(),
			"__openclaw": map[string]any{
				"seq": 10,
			},
		},
	})

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].sequence != 10 || entries[0].streamOrder != 10 {
		t.Fatalf("expected first entry to use seq 10, got %#v", entries[0])
	}
	if entries[1].sequence != 11 || entries[1].streamOrder != 11 {
		t.Fatalf("expected second entry to use seq 11, got %#v", entries[1])
	}
}

func TestPaginateOpenClawBackfillEntriesUsesCustomCursors(t *testing.T) {
	base := time.Date(2026, time.March, 12, 12, 0, 0, 0, time.UTC)
	entries := []openClawBackfillEntry{
		{messageID: "m1", timestamp: base.Add(1 * time.Second), sequence: 1, streamOrder: 1},
		{messageID: "m2", timestamp: base.Add(2 * time.Second), sequence: 2, streamOrder: 2},
		{messageID: "m3", timestamp: base.Add(3 * time.Second), sequence: 3, streamOrder: 3},
		{messageID: "m4", timestamp: base.Add(4 * time.Second), sequence: 4, streamOrder: 4},
		{messageID: "m5", timestamp: base.Add(5 * time.Second), sequence: 5, streamOrder: 5},
	}

	backward, cursor, hasMore := paginateOpenClawBackfillEntries(entries, bridgev2.FetchMessagesParams{
		Count:         2,
		AnchorMessage: &database.Message{ID: "m4", Timestamp: base.Add(4 * time.Second)},
	}, "", 0)
	if !hasMore || cursor != networkid.PaginationCursor("seq:2") {
		t.Fatalf("unexpected backward pagination result: cursor=%q hasMore=%v", cursor, hasMore)
	}
	if len(backward) != 2 || backward[0].sequence != 2 || backward[1].sequence != 3 {
		t.Fatalf("unexpected backward entries: %#v", backward)
	}

	forward, cursor, hasMore := paginateOpenClawBackfillEntries(entries, bridgev2.FetchMessagesParams{
		Count:   2,
		Forward: true,
		Cursor:  networkid.PaginationCursor("after:2"),
	}, openClawForwardHistoryCursorPrefix, 2)
	if !hasMore || cursor != networkid.PaginationCursor("after:4") {
		t.Fatalf("unexpected forward pagination result: cursor=%q hasMore=%v", cursor, hasMore)
	}
	if len(forward) != 2 || forward[0].sequence != 3 || forward[1].sequence != 4 {
		t.Fatalf("unexpected forward entries: %#v", forward)
	}
}

func TestAttachApprovalContextKeepsHintsAndPendingData(t *testing.T) {
	mgr := newOpenClawManager(&OpenClawClient{})
	t.Cleanup(func() {
		if mgr.approvalFlow != nil {
			mgr.approvalFlow.Close()
		}
	})

	mgr.attachApprovalContext("approval-1", "session-1", "agent-1", "turn-1", "tool-call-1", "exec_command")
	hint := mgr.approvalHint("approval-1")
	if hint.SessionKey != "session-1" || hint.AgentID != "agent-1" || hint.ToolCallID != "tool-call-1" || hint.TurnID != "turn-1" {
		t.Fatalf("unexpected stored approval hint: %#v", hint)
	}

	if _, created := mgr.approvalFlow.Register("approval-2", time.Minute, &openClawPendingApprovalData{}); !created {
		t.Fatal("expected pending approval to be created")
	}
	mgr.attachApprovalContext("approval-2", "session-2", "agent-2", "turn-2", "tool-call-2", "bash")
	pending := mgr.approvalFlow.Get("approval-2")
	if pending == nil || pending.Data == nil {
		t.Fatal("expected pending approval data to exist")
	}
	if pending.Data.SessionKey != "session-2" || pending.Data.AgentID != "agent-2" || pending.Data.ToolCallID != "tool-call-2" || pending.Data.ToolName != "bash" {
		t.Fatalf("unexpected pending approval data: %#v", pending.Data)
	}

	_ = agentremote.ErrApprovalUnknown
}

func TestOpenClawRequiredGatewayMethodsCoverCoreChatSessionFlow(t *testing.T) {
	required := make(map[string]struct{}, len(openClawRequiredGatewayMethods))
	for _, method := range openClawRequiredGatewayMethods {
		required[method] = struct{}{}
	}
	for _, method := range []string{"sessions.list", "chat.send"} {
		if _, ok := required[method]; !ok {
			t.Fatalf("expected required gateway methods to include %q", method)
		}
	}
}

func TestShouldEmitOpenClawRawAgentDataSuppressesAssistantTextSnapshots(t *testing.T) {
	if shouldEmitOpenClawRawAgentData("assistant", map[string]any{"text": "pretty good"}) {
		t.Fatal("expected assistant text snapshots to be suppressed")
	}
	if shouldEmitOpenClawRawAgentData("assistant", map[string]any{"delta": " good"}) {
		t.Fatal("expected assistant delta snapshots to be suppressed")
	}
	if !shouldEmitOpenClawRawAgentData("assistant", map[string]any{"phase": "start"}) {
		t.Fatal("expected non-text assistant payloads to remain available as raw data")
	}
	if !shouldEmitOpenClawRawAgentData("lifecycle", map[string]any{"phase": "start"}) {
		t.Fatal("expected non-assistant streams to keep raw data")
	}
}

func TestValidateGatewayCompatibilityAllowsOptionalGaps(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html>control-ui</html>"))
	}))
	defer server.Close()

	mgr := newOpenClawManager(&OpenClawClient{})
	gateway := newGatewayWSClient(gatewayConnectConfig{URL: server.URL})
	gateway.hello = &gatewayHello{
		Server: map[string]any{"version": "test"},
		Features: gatewayHelloFeatures{
			Methods: []string{"sessions.list", "chat.send", "chat.history"},
			Events:  []string{"chat"},
		},
	}

	report, err := mgr.validateGatewayCompatibility(context.Background(), gateway)
	if err != nil {
		t.Fatalf("validateGatewayCompatibility returned error: %v", err)
	}
	if report == nil || !report.Compatible() {
		t.Fatalf("expected compatibility report to accept optional gaps, got %#v", report)
	}
	if !containsString(report.MissingMethods, "agents.list") {
		t.Fatalf("expected optional missing methods to be reported, got %#v", report)
	}
	if !containsString(report.MissingEvents, "agent") {
		t.Fatalf("expected optional missing events to be reported, got %#v", report)
	}
}

func TestOpenClawBuildToolStreamUpdateFromStartArgs(t *testing.T) {
	update := openClawBuildToolStreamUpdate(time.UnixMilli(1_700_000_000_000), map[string]any{
		"phase":      "start",
		"toolCallId": "tool-1",
		"name":       "read",
		"args":       map[string]any{"path": "/tmp/example.txt"},
	})

	if len(update.Parts) != 1 {
		t.Fatalf("expected 1 part, got %#v", update.Parts)
	}
	part := update.Parts[0]
	if part["type"] != "tool-input-available" {
		t.Fatalf("unexpected part type: %#v", part)
	}
	if part["toolName"] != "read" || part["toolCallId"] != "tool-1" {
		t.Fatalf("unexpected tool identity: %#v", part)
	}
	input, _ := part["input"].(map[string]any)
	if input["path"] != "/tmp/example.txt" {
		t.Fatalf("unexpected tool input: %#v", input)
	}
}

func TestOpenClawBuildToolStreamUpdateFromStartWithoutArgs(t *testing.T) {
	update := openClawBuildToolStreamUpdate(time.UnixMilli(1_700_000_000_000), map[string]any{
		"phase":      "start",
		"toolCallId": "tool-2",
		"name":       "exec",
	})

	if len(update.Parts) != 1 {
		t.Fatalf("expected 1 part, got %#v", update.Parts)
	}
	part := update.Parts[0]
	if part["type"] != "tool-input-start" {
		t.Fatalf("unexpected part type: %#v", part)
	}
	if part["toolName"] != "exec" || part["toolCallId"] != "tool-2" {
		t.Fatalf("unexpected tool identity: %#v", part)
	}
}

func TestOpenClawBuildToolStreamUpdateFromPartialResult(t *testing.T) {
	update := openClawBuildToolStreamUpdate(time.UnixMilli(1_700_000_000_000), map[string]any{
		"phase":         "update",
		"toolCallId":    "tool-3",
		"name":          "fetch",
		"partialResult": map[string]any{"status": "running"},
	})

	if len(update.Parts) != 1 {
		t.Fatalf("expected 1 part, got %#v", update.Parts)
	}
	part := update.Parts[0]
	if part["type"] != "tool-output-available" {
		t.Fatalf("unexpected part type: %#v", part)
	}
	if preliminary, _ := part["preliminary"].(bool); !preliminary {
		t.Fatalf("expected preliminary output, got %#v", part)
	}
	output, _ := part["output"].(map[string]any)
	if output["status"] != "running" {
		t.Fatalf("unexpected partial output: %#v", output)
	}
}

func TestOpenClawBuildToolStreamUpdateFromFinalResult(t *testing.T) {
	update := openClawBuildToolStreamUpdate(time.UnixMilli(1_700_000_000_000), map[string]any{
		"phase":      "result",
		"toolCallId": "tool-4",
		"name":       "fetch",
		"result":     map[string]any{"status": 200},
	})

	if len(update.Parts) != 1 {
		t.Fatalf("expected 1 part, got %#v", update.Parts)
	}
	part := update.Parts[0]
	if part["type"] != "tool-output-available" {
		t.Fatalf("unexpected part type: %#v", part)
	}
	if preliminary, _ := part["preliminary"].(bool); preliminary {
		t.Fatalf("did not expect final output to be preliminary: %#v", part)
	}
	if !update.HasFinalOutput {
		t.Fatalf("expected final output marker, got %#v", update)
	}
	output, _ := update.FinalOutput.(map[string]any)
	if output["status"] != 200 {
		t.Fatalf("unexpected final output: %#v", output)
	}
}

func TestOpenClawBuildToolStreamUpdateFromErrorResult(t *testing.T) {
	update := openClawBuildToolStreamUpdate(time.UnixMilli(1_700_000_000_000), map[string]any{
		"phase":      "result",
		"toolCallId": "tool-5",
		"name":       "exec",
		"isError":    true,
		"result": map[string]any{
			"error": "permission denied",
		},
	})

	if len(update.Parts) != 1 {
		t.Fatalf("expected 1 part, got %#v", update.Parts)
	}
	part := update.Parts[0]
	if part["type"] != "tool-output-error" {
		t.Fatalf("unexpected part type: %#v", part)
	}
	if part["errorText"] != "permission denied" {
		t.Fatalf("unexpected error text: %#v", part)
	}
	if update.HasFinalOutput {
		t.Fatalf("did not expect final output on error: %#v", update)
	}
}

func TestLoadAllHistoryMessagesStopsWhenCursorRepeats(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("cursor") {
		case "":
			_, _ = w.Write([]byte(`{"messages":[{"role":"assistant","text":"first"}],"nextCursor":"stuck","hasMore":true}`))
		case "stuck":
			_, _ = w.Write([]byte(`{"messages":[{"role":"assistant","text":"second"}],"nextCursor":"stuck","hasMore":true}`))
		default:
			t.Fatalf("unexpected cursor %q", r.URL.Query().Get("cursor"))
		}
	}))
	defer server.Close()

	mgr := newOpenClawManager(&OpenClawClient{})
	gateway := newGatewayWSClient(gatewayConnectConfig{URL: server.URL})
	messages, err := mgr.loadAllHistoryMessages(context.Background(), gateway, "agent:main:test")
	if err != nil {
		t.Fatalf("loadAllHistoryMessages returned error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected repeated cursor to stop after 2 calls, got %d", calls)
	}
	if len(messages) != 2 {
		t.Fatalf("expected both fetched pages before loop exit, got %#v", messages)
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
