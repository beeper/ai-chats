package runtime

import (
	"strings"
	"testing"
)

func TestParseReplyDirectives_ExplicitOverridesCurrent(t *testing.T) {
	parsed := ParseReplyDirectives("[[reply_to_current]] hello [[reply_to:$evt123]]", "$current")
	if parsed.ReplyToID != "$evt123" {
		t.Fatalf("expected explicit reply id, got %q", parsed.ReplyToID)
	}
	if parsed.ReplyToCurrent {
		t.Fatalf("expected explicit reply id to override reply_to_current")
	}
	if parsed.Text != "hello" {
		t.Fatalf("unexpected cleaned text: %q", parsed.Text)
	}
}

func TestStreamingAccumulator_SplitDirective(t *testing.T) {
	acc := NewStreamingDirectiveAccumulator()
	if got := acc.Consume("hi [[reply_to_", false); got == nil || got.Text != "hi " {
		t.Fatalf("expected partial text before split directive, got %#v", got)
	}
	got := acc.Consume("current]] there", true)
	if got == nil {
		t.Fatalf("expected parsed final chunk")
	}
	if !got.HasReplyTag || !got.ReplyToCurrent {
		t.Fatalf("expected reply_to_current directive, got %#v", got)
	}
	if strings.TrimSpace(got.Text) != "there" {
		t.Fatalf("expected directive-stripped trailing text, got %q", got.Text)
	}
}

func TestSanitizeChatMessageForDisplay_User(t *testing.T) {
	input := "[Matrix] Alice: hello\n[message_id: $abc]\nConversation info (untrusted metadata):\n```json\n{\"a\":1}\n```"
	out := SanitizeChatMessageForDisplay(input, true)
	if out != "Alice: hello" {
		t.Fatalf("unexpected sanitized output: %q", out)
	}
}

func TestFinalizeInboundContext_BodyFallbacks(t *testing.T) {
	ctx := FinalizeInboundContext(InboundContext{RawBody: "raw"})
	if ctx.BodyForAgent != "raw" {
		t.Fatalf("expected BodyForAgent fallback to raw, got %q", ctx.BodyForAgent)
	}
	if ctx.BodyForCommands != "raw" {
		t.Fatalf("expected BodyForCommands fallback to raw, got %q", ctx.BodyForCommands)
	}
}

func TestApplyReplyToMode_First(t *testing.T) {
	in := []ReplyPayload{
		{ReplyToID: "$a", ReplyToTag: true},
		{ReplyToID: "$b", ReplyToTag: true},
	}
	out := ApplyReplyToMode(in, ReplyThreadPolicy{Mode: ReplyToModeFirst})
	if out[0].ReplyToID != "$a" {
		t.Fatalf("expected first reply id to be preserved")
	}
	if out[1].ReplyToID != "" {
		t.Fatalf("expected second reply id to be stripped in first mode")
	}
}

func TestQueueFallbackToolCompactionDecisions(t *testing.T) {
	queue := DecideQueueAction(QueueModeInterrupt, true, false)
	if queue.Action != QueueActionInterruptAndRun {
		t.Fatalf("unexpected queue decision: %#v", queue)
	}
	if cls := ClassifyFallbackError(assertErr("rate limit exceeded")); cls != FailureClassRateLimit {
		t.Fatalf("unexpected fallback classification: %s", cls)
	}
	tool := DecideToolApproval(ToolPolicyInput{ToolName: "message", ToolKind: "mcp", RequireForMCP: true})
	if tool.State != ToolApprovalRequired {
		t.Fatalf("expected required tool approval, got %#v", tool)
	}
	comp := ApplyCompaction(CompactionInput{
		Messages:      []string{"aaaa", "bbbb", "cccc"},
		MaxChars:      6,
		ProtectedTail: 1,
	})
	if !comp.Decision.Applied || comp.DroppedCount == 0 {
		t.Fatalf("expected compaction to drop messages, got %#v", comp.Decision)
	}
	if mode, ok := NormalizeQueueMode("steer+backlog"); !ok || mode != QueueModeSteerBacklog {
		t.Fatalf("expected normalized steer+backlog mode, got mode=%q ok=%v", mode, ok)
	}
	if drop, ok := NormalizeQueueDropPolicy("summarize"); !ok || drop != QueueDropSummarize {
		t.Fatalf("expected normalized summarize drop policy, got drop=%q ok=%v", drop, ok)
	}
	overflow := ResolveQueueOverflow(2, 2, QueueDropSummarize)
	if !overflow.KeepNew || overflow.ItemsToDrop != 1 || !overflow.ShouldSummarize {
		t.Fatalf("unexpected overflow decision: %#v", overflow)
	}
	if d := DecideFallback(assertErr("invalid_api_key")); d.Action != FallbackActionAbort {
		t.Fatalf("expected auth fallback action abort, got %#v", d)
	}
}

func TestNormalizeMessageID(t *testing.T) {
	if got := NormalizeMessageID("[message_id: $abc:example.com]"); got != "$abc:example.com" {
		t.Fatalf("expected normalized message id, got %q", got)
	}
	if got := NormalizeMessageID("reply [message_id: $abc:example.com]"); got != "$abc:example.com" {
		t.Fatalf("expected inline normalized message id, got %q", got)
	}
	if got := NormalizeMessageID("[message_id: bad id]"); got != "" {
		t.Fatalf("expected invalid message id to normalize empty, got %q", got)
	}
}

func TestAbortTriggerNormalization(t *testing.T) {
	if !IsAbortTriggerText("STOP PLEASE!!!") {
		t.Fatalf("expected normalized abort trigger to match")
	}
	if IsAbortTriggerText("continue") {
		t.Fatalf("did not expect non-abort text to match")
	}
}

type simpleErr string

func (e simpleErr) Error() string { return string(e) }

func assertErr(text string) error { return simpleErr(text) }
