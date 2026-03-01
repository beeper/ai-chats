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
}

type simpleErr string

func (e simpleErr) Error() string { return string(e) }

func assertErr(text string) error { return simpleErr(text) }
