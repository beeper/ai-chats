package runtime

import (
	"errors"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
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

func TestStreamingAccumulator_ReplyTagStickyAcrossChunks(t *testing.T) {
	acc := NewStreamingDirectiveAccumulator()
	if got := acc.Consume("[[reply_to_current]]", false); got != nil {
		t.Fatalf("expected tag-only chunk to be deferred, got %#v", got)
	}
	first := acc.Consume("first", false)
	if first == nil || !first.HasReplyTag || !first.ReplyToCurrent {
		t.Fatalf("expected first renderable chunk to carry reply tag, got %#v", first)
	}
	second := acc.Consume("second", false)
	if second == nil || !second.HasReplyTag || !second.ReplyToCurrent {
		t.Fatalf("expected sticky reply tag on subsequent chunk, got %#v", second)
	}
}

func TestSanitizeChatMessageForDisplay_User(t *testing.T) {
	input := "[Matrix room Thu 2025-01-02T03:04Z] Alice: hello\n[message_id: $abc]\nConversation info (untrusted metadata):\n```json\n{\"a\":1}\n```"
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
	if !comp.Decision.Applied || comp.Decision.DroppedCount == 0 {
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
	if cls := ClassifyFallbackError(assertErr(`403 Forbidden {"message":"This feature requires the bridge:ai feature flag","type":"invalid_request_error","code":"access_denied"}`)); cls != FailureClassProviderHard {
		t.Fatalf("expected access_denied 403 to classify as provider hard failure, got %s", cls)
	}
	if d := DecideFallback(assertErr(`403 Forbidden {"message":"This feature requires the bridge:ai feature flag","type":"invalid_request_error","code":"access_denied"}`)); d.Action != FallbackActionFailover {
		t.Fatalf("expected access_denied fallback action failover, got %#v", d)
	}
	if cls := ClassifyFallbackError(assertErr(`403 Forbidden {"message":"This model is not available","code":"model_not_found"}`)); cls != FailureClassProviderHard {
		t.Fatalf("expected model_not_found 403 to classify as provider hard failure, got %s", cls)
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

func TestSilentReplySemantics(t *testing.T) {
	if !IsSilentReplyText("NO_REPLY", SilentReplyToken) {
		t.Fatal("expected exact NO_REPLY token to be silent")
	}
	if IsSilentReplyText("NO_REPLY but with text", SilentReplyToken) {
		t.Fatal("did not expect substantive text to be silent")
	}
	if !IsSilentReplyPrefixText("NO_RE", SilentReplyToken) {
		t.Fatal("expected uppercase underscore prefix to match")
	}
	if IsSilentReplyPrefixText("No", SilentReplyToken) {
		t.Fatal("did not expect ambiguous natural-language prefix to match")
	}
}

func TestStripMessageIDHintLines_LiteralBehavior(t *testing.T) {
	if got := StripMessageIDHintLines("hi\n[message_id: abc123]"); got != "hi" {
		t.Fatalf("expected full-line hint to be stripped, got %q", got)
	}
	if got := StripMessageIDHintLines("I typed [message_id: abc123] on purpose"); got != "I typed [message_id: abc123] on purpose" {
		t.Fatalf("expected inline message_id to be preserved, got %q", got)
	}
	if got := StripMessageIDHintLines("[MESSAGE_ID: abc123]"); got != "[MESSAGE_ID: abc123]" {
		t.Fatalf("expected case-sensitive guard behavior to preserve uppercase hint, got %q", got)
	}
}

func TestCompactPromptOnOverflow_UsesMaxHistoryShareBudget(t *testing.T) {
	prompt := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("system"),
		openai.UserMessage(strings.Repeat("A", 200)),
		openai.AssistantMessage(strings.Repeat("B", 200)),
		openai.UserMessage(strings.Repeat("C", 200)),
		openai.AssistantMessage(strings.Repeat("D", 200)),
		openai.UserMessage(strings.Repeat("E", 200)),
		openai.AssistantMessage(strings.Repeat("F", 200)),
	}
	result := CompactPromptOnOverflow(OverflowCompactionInput{
		Prompt:              prompt,
		ContextWindowTokens: 200,
		RequestedTokens:     200,
		ReserveTokens:       0,
		MaxHistoryShare:     0.1,
		ProtectedTail:       2,
	})
	if !result.Decision.Applied || result.Decision.DroppedCount == 0 {
		t.Fatalf("expected compaction decision to drop history, got %#v", result.Decision)
	}
	if len(result.Prompt) > len(prompt) {
		t.Fatalf("expected compacted prompt to shrink, before=%d after=%d", len(prompt), len(result.Prompt))
	}
}

func TestApplyPruningDefaults_ReserveFloorOnlyWhenUnset(t *testing.T) {
	withReserve := ApplyPruningDefaults(&PruningConfig{ReserveTokens: 777})
	if withReserve.ReserveTokens != 777 {
		t.Fatalf("expected explicit reserve tokens to be preserved, got %d", withReserve.ReserveTokens)
	}
	if withReserve.ReserveTokensFloor != 0 {
		t.Fatalf("expected reserve floor to stay unset when reserve tokens is explicit, got %d", withReserve.ReserveTokensFloor)
	}

	unset := ApplyPruningDefaults(&PruningConfig{})
	if unset.ReserveTokens <= 0 {
		t.Fatalf("expected default reserve tokens, got %d", unset.ReserveTokens)
	}
	if unset.ReserveTokensFloor <= 0 {
		t.Fatalf("expected default reserve floor when reserve tokens are unset, got %d", unset.ReserveTokensFloor)
	}
}

func TestApplyPruningDefaults_CompactionKnobs(t *testing.T) {
	cfg := ApplyPruningDefaults(&PruningConfig{
		CompactionMode:  "DEFAULT",
		MaxHistoryShare: 0.99,
	})
	if cfg.CompactionMode != "default" {
		t.Fatalf("expected normalized compaction mode default, got %q", cfg.CompactionMode)
	}
	if cfg.KeepRecentTokens <= 0 {
		t.Fatalf("expected keep recent tokens defaulted, got %d", cfg.KeepRecentTokens)
	}
	if cfg.MaxHistoryShare > 0.9 {
		t.Fatalf("expected max history share clamp <= 0.9, got %v", cfg.MaxHistoryShare)
	}
	if cfg.SummarizationEnabled == nil || !*cfg.SummarizationEnabled {
		t.Fatalf("expected summarization enabled by default")
	}
	if cfg.MaxSummaryTokens <= 0 {
		t.Fatalf("expected max summary tokens defaulted, got %d", cfg.MaxSummaryTokens)
	}
	if strings.TrimSpace(cfg.PostCompactionRefresh) == "" {
		t.Fatal("expected post compaction refresh prompt defaulted")
	}
}

func TestCompactPromptOnOverflow_InsertsSummaryAndRefresh(t *testing.T) {
	prompt := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("sys"),
		openai.UserMessage(strings.Repeat("U", 350)),
		openai.AssistantMessage(strings.Repeat("A", 350)),
		openai.UserMessage(strings.Repeat("X", 350)),
		openai.AssistantMessage(strings.Repeat("Y", 350)),
	}
	result := CompactPromptOnOverflow(OverflowCompactionInput{
		Prompt:              prompt,
		ContextWindowTokens: 300,
		RequestedTokens:     600,
		CurrentPromptTokens: 600,
		ReserveTokens:       100,
		KeepRecentTokens:    120,
		CompactionMode:      "safeguard",
		Summarization:       true,
		MaxSummaryTokens:    120,
		RefreshPrompt:       "[Post-compaction context refresh]\nRe-anchor.",
		MaxHistoryShare:     0.5,
		ProtectedTail:       2,
	})
	if !result.Decision.Applied || !result.Success {
		t.Fatalf("expected successful compaction with dropped history, got decision=%#v success=%v", result.Decision, result.Success)
	}
	sawSummary := false
	sawRefresh := false
	for _, msg := range result.Prompt {
		if msg.OfSystem == nil {
			continue
		}
		text := ExtractSystemContent(msg.OfSystem.Content)
		if strings.Contains(text, "[Compaction summary of earlier context]") {
			sawSummary = true
		}
		if strings.Contains(text, "[Post-compaction context refresh]") {
			sawRefresh = true
		}
	}
	if !sawSummary {
		t.Fatal("expected compaction summary system message")
	}
	if !sawRefresh {
		t.Fatal("expected post-compaction refresh system message")
	}
}

func assertErr(text string) error { return errors.New(text) }
