package connector

import (
	"context"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/rs/zerolog"
)

func TestCompactorCreation(t *testing.T) {
	log := zerolog.Nop()
	config := DefaultCompactionConfig()

	compactor := NewCompactor(nil, log, config)
	if compactor == nil {
		t.Fatal("NewCompactor returned nil")
	}

	if compactor.config == nil {
		t.Error("Compactor config is nil")
	}

	if compactor.config.MaxHistoryShare != 0.5 {
		t.Errorf("Expected MaxHistoryShare 0.5, got %f", compactor.config.MaxHistoryShare)
	}
}

func TestCompactorSplitMessages(t *testing.T) {
	log := zerolog.Nop()
	config := DefaultCompactionConfig()
	config.MaxHistoryShare = 0.3 // Force compaction by using low threshold

	compactor := NewCompactor(nil, log, config)

	// Create test messages
	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("You are a helpful assistant."),
		openai.UserMessage("Hello, how are you?"),
		openai.AssistantMessage("I'm doing well, thank you!"),
		openai.UserMessage("What is 2+2?"),
		openai.AssistantMessage("2+2 equals 4."),
		openai.UserMessage("Thanks!"),
		openai.AssistantMessage("You're welcome!"),
	}

	// With a small context window, should split messages
	toSummarize, toKeep := compactor.splitMessagesForCompaction(messages, 1000)

	// System prompt should always be kept
	hasSystem := false
	for _, msg := range toKeep {
		if msg.OfSystem != nil {
			hasSystem = true
			break
		}
	}
	if !hasSystem {
		t.Error("System message should be kept, not summarized")
	}

	// Recent assistant messages should be protected
	if len(toKeep) < 3 {
		t.Errorf("Expected at least 3 messages kept (including recent assistants), got %d", len(toKeep))
	}

	t.Logf("Split result: %d to summarize, %d to keep", len(toSummarize), len(toKeep))
}

func TestCompactorFallbackSummary(t *testing.T) {
	log := zerolog.Nop()
	config := DefaultCompactionConfig()
	compactor := NewCompactor(nil, log, config)

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage("Hello"),
		openai.AssistantMessage("Hi there!"),
		openai.UserMessage("How are you?"),
		openai.AssistantMessage("I'm good!"),
	}

	summary := compactor.generateFallbackSummary(messages)

	if summary == "" {
		t.Error("Fallback summary should not be empty")
	}

	if len(summary) < 20 {
		t.Errorf("Fallback summary too short: %s", summary)
	}

	t.Logf("Fallback summary: %s", summary)
}

func TestCompactionHooks(t *testing.T) {
	hookCalled := false
	hook := CompactionBeforeHook(func(ctx context.Context, hookCtx *CompactionHookContext) (*CompactionHookResult, error) {
		hookCalled = true
		return &CompactionHookResult{Skip: true}, nil
	})
	globalCompactionHooks.mu.Lock()
	prev := globalCompactionHooks.beforeHooks
	globalCompactionHooks.beforeHooks = append(prev, hook)
	globalCompactionHooks.mu.Unlock()
	defer func() {
		globalCompactionHooks.mu.Lock()
		globalCompactionHooks.beforeHooks = prev
		globalCompactionHooks.mu.Unlock()
	}()

	result, err := globalCompactionHooks.runBeforeHooks(context.Background(), &CompactionHookContext{
		SessionID:    "test",
		MessageCount: 10,
		TokenCount:   1000,
	})

	if err != nil {
		t.Errorf("Hook returned error: %v", err)
	}

	if !hookCalled {
		t.Error("Before hook was not called")
	}

	if result == nil || !result.Skip {
		t.Error("Hook result should indicate skip")
	}
}

func TestCompactionConfig(t *testing.T) {
	config := DefaultCompactionConfig()

	if config.PruningConfig == nil {
		t.Error("PruningConfig should not be nil in default config")
	}

	if config.SummarizationEnabled == nil || !*config.SummarizationEnabled {
		t.Error("SummarizationEnabled should default to true")
	}

	if config.MaxSummaryTokens != 500 {
		t.Errorf("Expected MaxSummaryTokens 500, got %d", config.MaxSummaryTokens)
	}

	if config.MaxHistoryShare != 0.5 {
		t.Errorf("Expected MaxHistoryShare 0.5, got %f", config.MaxHistoryShare)
	}

	if config.ReserveTokens != 20000 {
		t.Errorf("Expected ReserveTokens 20000, got %d", config.ReserveTokens)
	}
	if config.IdentifierPolicy != "strict" {
		t.Errorf("Expected IdentifierPolicy strict, got %q", config.IdentifierPolicy)
	}
}

func TestCompactionEventTypes(t *testing.T) {
	if CompactionEventStart != "compaction_start" {
		t.Errorf("Expected CompactionEventStart to be 'compaction_start', got %s", CompactionEventStart)
	}

	if CompactionEventEnd != "compaction_end" {
		t.Errorf("Expected CompactionEventEnd to be 'compaction_end', got %s", CompactionEventEnd)
	}

}

func TestEstimateTotalTokens(t *testing.T) {
	log := zerolog.Nop()
	config := DefaultCompactionConfig()
	compactor := NewCompactor(nil, log, config)

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("You are a helpful assistant."),
		openai.UserMessage("Hello, how are you?"),
		openai.AssistantMessage("I'm doing well, thank you for asking!"),
	}

	tokens := compactor.estimateTotalTokens(messages)

	if tokens <= 0 {
		t.Error("Token estimate should be positive")
	}

	// Rough estimate: ~20 words = ~25-30 tokens
	if tokens < 10 || tokens > 100 {
		t.Errorf("Token estimate seems off: %d", tokens)
	}

	t.Logf("Estimated tokens: %d", tokens)
}

func TestCompactorSplitMessages_ProtectsLatestUserWithFewAssistants(t *testing.T) {
	compactor := NewCompactor(nil, zerolog.Nop(), &CompactionConfig{
		PruningConfig:   DefaultPruningConfig(),
		MaxHistoryShare: 0.1,
	})

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("system"),
		openai.UserMessage(strings.Repeat("old user content ", 20)),
		openai.AssistantMessage(strings.Repeat("old assistant content ", 20)),
		openai.UserMessage("latest user request"),
	}

	toSummarize, toKeep := compactor.splitMessagesForCompaction(messages, 20)
	if len(toSummarize) == 0 {
		t.Fatalf("expected some messages to summarize")
	}
	if containsUserMessage(toSummarize, "latest user request") {
		t.Fatalf("latest user request must not be summarized")
	}
	if !containsUserMessage(toKeep, "latest user request") {
		t.Fatalf("latest user request must be kept verbatim")
	}
}

func TestCompactorSplitMessages_ExcludesLatestUserUnderAggressiveCompaction(t *testing.T) {
	compactor := NewCompactor(nil, zerolog.Nop(), &CompactionConfig{
		PruningConfig:   DefaultPruningConfig(),
		MaxHistoryShare: 0.1,
	})

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("system"),
		openai.UserMessage(strings.Repeat("turn one ", 30)),
		openai.UserMessage(strings.Repeat("turn two ", 30)),
		openai.UserMessage("latest user request"),
	}

	toSummarize, toKeep := compactor.splitMessagesForCompaction(messages, 20)
	if containsUserMessage(toSummarize, "latest user request") {
		t.Fatalf("latest user request must not be summarized")
	}
	if !containsUserMessage(toKeep, "latest user request") {
		t.Fatalf("latest user request must remain in kept context")
	}
}

func TestCompactorSplitMessages_NoSummaryWhenOnlyLatestUserIsAvailable(t *testing.T) {
	compactor := NewCompactor(nil, zerolog.Nop(), &CompactionConfig{
		PruningConfig:   DefaultPruningConfig(),
		MaxHistoryShare: 0.1,
	})

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("system"),
		openai.UserMessage(strings.Repeat("latest user request ", 50)),
	}

	toSummarize, toKeep := compactor.splitMessagesForCompaction(messages, 20)
	if len(toSummarize) != 0 {
		t.Fatalf("expected no messages to summarize, got %d", len(toSummarize))
	}
	if len(toKeep) != len(messages) {
		t.Fatalf("expected to keep all messages, got keep=%d want=%d", len(toKeep), len(messages))
	}
}

func containsUserMessage(messages []openai.ChatCompletionMessageParamUnion, want string) bool {
	for _, msg := range messages {
		if msg.OfUser == nil || !msg.OfUser.Content.OfString.Valid() {
			continue
		}
		if msg.OfUser.Content.OfString.Value == want {
			return true
		}
	}
	return false
}

func TestCompactorBuildSummarizationInstructions_StrictDefault(t *testing.T) {
	compactor := NewCompactor(nil, zerolog.Nop(), &CompactionConfig{
		PruningConfig:      DefaultPruningConfig(),
		CustomInstructions: "Focus on TODOs.",
	})

	instructions := compactor.buildSummarizationInstructions()
	if !strings.Contains(instructions, "Preserve non-secret opaque identifiers exactly as written") {
		t.Fatalf("expected strict identifier preservation instructions, got: %q", instructions)
	}
	if !strings.Contains(instructions, "Focus on TODOs.") {
		t.Fatalf("expected custom instructions to be included, got: %q", instructions)
	}
}

func TestCompactorBuildSummarizationInstructions_IdentifierOff(t *testing.T) {
	compactor := NewCompactor(nil, zerolog.Nop(), &CompactionConfig{
		PruningConfig:      DefaultPruningConfig(),
		IdentifierPolicy:   "off",
		CustomInstructions: "Only include decisions.",
	})

	instructions := compactor.buildSummarizationInstructions()
	if strings.Contains(instructions, "Preserve all opaque identifiers exactly as written") {
		t.Fatalf("did not expect identifier-preservation instructions when policy=off")
	}
	if instructions != "Only include decisions." {
		t.Fatalf("unexpected instructions: %q", instructions)
	}
}

func TestCompactorBuildSummarizationInstructions_IdentifierCustom(t *testing.T) {
	compactor := NewCompactor(nil, zerolog.Nop(), &CompactionConfig{
		PruningConfig:          DefaultPruningConfig(),
		IdentifierPolicy:       "custom",
		IdentifierInstructions: "Keep JIRA and commit hashes verbatim.",
		CustomInstructions:     "Highlight blockers.",
	})

	instructions := compactor.buildSummarizationInstructions()
	if !strings.Contains(instructions, "Keep JIRA and commit hashes verbatim.") {
		t.Fatalf("expected custom identifier instructions, got: %q", instructions)
	}
	if !strings.Contains(instructions, "Highlight blockers.") {
		t.Fatalf("expected custom focus instructions, got: %q", instructions)
	}
}
