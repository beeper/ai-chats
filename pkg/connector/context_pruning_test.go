package connector

import (
	"strings"
	"testing"
	"time"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
	"github.com/openai/openai-go/v3"
)

func TestPruneContext(t *testing.T) {
	// Helper to create user messages
	userMsg := func(content string) openai.ChatCompletionMessageParamUnion {
		return openai.UserMessage(content)
	}

	// Helper to create assistant messages
	assistantMsg := func(content string) openai.ChatCompletionMessageParamUnion {
		return openai.AssistantMessage(content)
	}

	// Helper to create system messages
	systemMsg := func(content string) openai.ChatCompletionMessageParamUnion {
		return openai.SystemMessage(content)
	}

	// Helper to create tool result messages
	toolResultMsg := func(content, toolCallID string) openai.ChatCompletionMessageParamUnion {
		return openai.ToolMessage(content, toolCallID)
	}

	t.Run("disabled config returns original", func(t *testing.T) {
		prompt := []openai.ChatCompletionMessageParamUnion{
			systemMsg("System"),
			userMsg("Hello"),
			assistantMsg("Hi!"),
		}
		config := &airuntime.PruningConfig{Enabled: false}

		result := airuntime.PruneContext(prompt, config, 4096)

		if len(result) != len(prompt) {
			t.Errorf("Expected %d messages, got %d", len(prompt), len(result))
		}
	})

	t.Run("nil config returns original", func(t *testing.T) {
		prompt := []openai.ChatCompletionMessageParamUnion{
			systemMsg("System"),
			userMsg("Hello"),
		}

		result := airuntime.PruneContext(prompt, nil, 4096)

		if len(result) != len(prompt) {
			t.Errorf("Expected %d messages, got %d", len(prompt), len(result))
		}
	})

	t.Run("under soft trim ratio returns original", func(t *testing.T) {
		prompt := []openai.ChatCompletionMessageParamUnion{
			systemMsg("System"),
			userMsg("Hello"),
			assistantMsg("Hi there!"),
		}
		config := &airuntime.PruningConfig{
			Enabled:       true,
			SoftTrimRatio: 0.9, // Very high threshold
		}

		result := airuntime.PruneContext(prompt, config, 100000) // Large context window

		if len(result) != len(prompt) {
			t.Errorf("Expected %d messages, got %d", len(prompt), len(result))
		}
	})

	t.Run("soft trims large tool results", func(t *testing.T) {
		largeContent := strings.Repeat("x", 10000) // 10k chars

		prompt := []openai.ChatCompletionMessageParamUnion{
			systemMsg("System"),
			userMsg("Run tool"),
			assistantMsg("Running..."),
			toolResultMsg(largeContent, "call_123"),
			assistantMsg("Done"),
			userMsg("Thanks"),
		}
		config := &airuntime.PruningConfig{
			Enabled:            true,
			SoftTrimRatio:      0.1, // Low threshold to trigger pruning
			HardClearRatio:     0.9, // High to avoid hard clear
			KeepLastAssistants: 1,
			SoftTrimMaxChars:   4000,
			SoftTrimHeadChars:  1500,
			SoftTrimTailChars:  1500,
		}

		result := airuntime.PruneContext(prompt, config, 5000) // Small context window

		// Find the tool result
		var toolContent string
		for _, msg := range result {
			if msg.OfTool != nil {
				toolContent = airuntime.ExtractToolContent(msg.OfTool.Content)
				break
			}
		}

		if toolContent == "" {
			t.Fatal("Tool result should be preserved")
		}

		// Should be trimmed
		if len(toolContent) >= len(largeContent) {
			t.Errorf("Tool result should be trimmed, got %d chars", len(toolContent))
		}

		// Should contain trim notice
		if !strings.Contains(toolContent, "trimmed") {
			t.Error("Trimmed content should contain notice")
		}
	})

	t.Run("hard clears when over threshold", func(t *testing.T) {
		// Create multiple large tool results to exceed MinPrunableChars after soft trim
		largeContent1 := strings.Repeat("x", 30000)
		largeContent2 := strings.Repeat("y", 30000)

		prompt := []openai.ChatCompletionMessageParamUnion{
			systemMsg("System"),
			userMsg("First"),
			assistantMsg("Response 1"),
			toolResultMsg(largeContent1, "call_1"),
			userMsg("Second"),
			assistantMsg("Response 2"),
			toolResultMsg(largeContent2, "call_2"),
			userMsg("Third"),
			assistantMsg("Response 3"), // Protected by keepLastAssistants
			userMsg("Latest"),
		}
		enabled := true
		config := &airuntime.PruningConfig{
			Enabled:              true,
			SoftTrimRatio:        0.01, // Very low to always trigger
			HardClearRatio:       0.01, // Very low to always trigger
			KeepLastAssistants:   1,
			MinPrunableChars:     1000, // Low threshold so hard clear kicks in
			SoftTrimMaxChars:     2000, // Trigger soft trim first
			SoftTrimHeadChars:    500,
			SoftTrimTailChars:    500,
			HardClearEnabled:     &enabled,
			HardClearPlaceholder: "[CLEARED]",
		}

		result := airuntime.PruneContext(prompt, config, 500) // Very small context window

		// Find cleared or trimmed tool result
		var foundCleared, foundTrimmed bool
		for _, msg := range result {
			if msg.OfTool != nil {
				content := airuntime.ExtractToolContent(msg.OfTool.Content)
				if content == "[CLEARED]" {
					foundCleared = true
				}
				if strings.Contains(content, "trimmed") {
					foundTrimmed = true
				}
			}
		}

		// Either hard clear or soft trim should have happened
		if !foundCleared && !foundTrimmed {
			t.Error("Expected at least one tool result to be cleared or trimmed")
		}
	})

	t.Run("respects keepLastAssistants", func(t *testing.T) {
		prompt := []openai.ChatCompletionMessageParamUnion{
			systemMsg("System"),
			userMsg("First"),
			assistantMsg("Old response"),
			toolResultMsg(strings.Repeat("x", 10000), "call_old"),
			userMsg("Second"),
			assistantMsg("Middle response"),
			toolResultMsg(strings.Repeat("y", 10000), "call_mid"),
			userMsg("Third"),
			assistantMsg("Recent response"),
			toolResultMsg(strings.Repeat("z", 10000), "call_new"),
			userMsg("Latest"),
		}
		config := &airuntime.PruningConfig{
			Enabled:            true,
			SoftTrimRatio:      0.1,
			HardClearRatio:     0.9,
			KeepLastAssistants: 2, // Protect last 2 assistant messages
			SoftTrimMaxChars:   4000,
		}

		result := airuntime.PruneContext(prompt, config, 2000)

		// Count how many tool results were trimmed
		var trimmedCount int
		for _, msg := range result {
			if msg.OfTool != nil {
				content := airuntime.ExtractToolContent(msg.OfTool.Content)
				if strings.Contains(content, "trimmed") {
					trimmedCount++
				}
			}
		}

		// Only the first tool result should be trimmed (not protected)
		if trimmedCount > 1 {
			t.Errorf("Expected only 1 tool result trimmed (old one), got %d", trimmedCount)
		}
	})

	t.Run("never prunes before first user message", func(t *testing.T) {
		// Simulate bootstrap files before first user message
		bootstrapContent := strings.Repeat("BOOTSTRAP", 10000)
		prompt := []openai.ChatCompletionMessageParamUnion{
			systemMsg("System"),
			assistantMsg("Loading identity..."),
			toolResultMsg(bootstrapContent, "call_bootstrap"), // Should be protected
			userMsg("Hello"),                                  // First user message
			assistantMsg("Hi!"),
			toolResultMsg(strings.Repeat("x", 10000), "call_after"),
			userMsg("Latest"),
		}
		config := &airuntime.PruningConfig{
			Enabled:            true,
			SoftTrimRatio:      0.1,
			HardClearRatio:     0.9,
			KeepLastAssistants: 1,
			SoftTrimMaxChars:   4000,
		}

		result := airuntime.PruneContext(prompt, config, 2000)

		// Find the bootstrap tool result
		var bootstrapFound bool
		for _, msg := range result {
			if msg.OfTool != nil && msg.OfTool.ToolCallID == "call_bootstrap" {
				content := airuntime.ExtractToolContent(msg.OfTool.Content)
				// Should NOT be trimmed
				if !strings.Contains(content, "trimmed") {
					bootstrapFound = true
				}
				break
			}
		}

		if !bootstrapFound {
			t.Error("Bootstrap tool result before first user message should not be pruned")
		}
	})
}

func TestToolPatternMatching(t *testing.T) {
	t.Run("exact match", func(t *testing.T) {
		config := &airuntime.PruningConfig{
			ToolsAllow: []string{"list_models"},
		}
		pred := airuntime.BuildToolPrunablePredicate(config)

		if !pred("list_models") {
			t.Error("Should match exact 'list_models'")
		}
		if pred("list_models_background") {
			t.Error("Should not match 'list_models_background' with exact pattern")
		}
	})

	t.Run("wildcard suffix", func(t *testing.T) {
		config := &airuntime.PruningConfig{
			ToolsAllow: []string{"list_*"},
		}
		pred := airuntime.BuildToolPrunablePredicate(config)

		if !pred("list_agents") {
			t.Error("Should match 'list_agents'")
		}
		if !pred("list_models") {
			t.Error("Should match 'list_models'")
		}
		if pred("read") {
			t.Error("Should not match 'read'")
		}
	})

	t.Run("wildcard prefix", func(t *testing.T) {
		config := &airuntime.PruningConfig{
			ToolsAllow: []string{"*_search"},
		}
		pred := airuntime.BuildToolPrunablePredicate(config)

		if !pred("web_search") {
			t.Error("Should match 'web_search'")
		}
		if !pred("memory_search") {
			t.Error("Should match 'memory_search'")
		}
		if pred("search") {
			t.Error("Should not match 'search' (no prefix)")
		}
	})

	t.Run("deny takes precedence", func(t *testing.T) {
		config := &airuntime.PruningConfig{
			ToolsAllow: []string{"*"},
			ToolsDeny:  []string{"read"},
		}
		pred := airuntime.BuildToolPrunablePredicate(config)

		if pred("read") {
			t.Error("Should not match 'read' (denied)")
		}
		if !pred("write") {
			t.Error("Should match 'write'")
		}
	})

	t.Run("empty allow means all allowed", func(t *testing.T) {
		config := &airuntime.PruningConfig{
			ToolsAllow: nil,
			ToolsDeny:  nil,
		}
		pred := airuntime.BuildToolPrunablePredicate(config)

		if !pred("anything") {
			t.Error("Should match any tool when allow list is empty")
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		config := &airuntime.PruningConfig{
			ToolsAllow: []string{"LIST_*"},
		}
		pred := airuntime.BuildToolPrunablePredicate(config)

		if !pred("list_models") {
			t.Error("Should match lowercase 'list_models'")
		}
		if !pred("LIST_MODELS") {
			t.Error("Should match uppercase 'LIST_MODELS'")
		}
	})
}

func TestSoftTrimToolResult(t *testing.T) {
	config := airuntime.DefaultPruningConfig()

	t.Run("does not trim small content", func(t *testing.T) {
		content := "Small content"
		result := airuntime.SoftTrimToolResult(content, config)
		if result != content {
			t.Error("Small content should not be trimmed")
		}
	})

	t.Run("trims large content with head and tail", func(t *testing.T) {
		content := strings.Repeat("H", 2000) +
			strings.Repeat("M", 5000) +
			strings.Repeat("T", 2000)

		result := airuntime.SoftTrimToolResult(content, config)

		if len(result) >= len(content) {
			t.Error("Large content should be trimmed")
		}
		if !strings.HasPrefix(result, strings.Repeat("H", 100)) {
			t.Error("Should preserve head")
		}
		if !strings.Contains(result, "trimmed") {
			t.Error("Should contain trim notice")
		}
	})
}

func TestSmartTruncatePrompt(t *testing.T) {
	t.Run("returns nil for very short prompts", func(t *testing.T) {
		prompt := []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("System"),
			openai.UserMessage("Hello"),
		}

		result := airuntime.SmartTruncatePrompt(prompt, 0.5)

		if result != nil {
			t.Error("Should return nil for 2-message prompt")
		}
	})

	t.Run("aggressive pruning for fallback", func(t *testing.T) {
		prompt := []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("System"),
			openai.UserMessage("First"),
			openai.AssistantMessage("Response"),
			openai.ToolMessage(strings.Repeat("x", 10000), "call_1"),
			openai.UserMessage("Latest"),
		}

		result := airuntime.SmartTruncatePrompt(prompt, 0.5)

		if result == nil {
			t.Fatal("Should return pruned result")
		}
		if len(result) < 2 {
			t.Error("Should preserve at least 2 messages")
		}
	})
}

func TestDefaultPruningConfig(t *testing.T) {
	config := airuntime.DefaultPruningConfig()

	if config.Mode != "cache-ttl" {
		t.Errorf("Expected Mode cache-ttl, got %q", config.Mode)
	}
	if config.TTL != time.Hour {
		t.Errorf("Expected TTL 1h, got %v", config.TTL)
	}
	if !config.Enabled {
		t.Errorf("Expected Enabled true, got %v", config.Enabled)
	}
	if config.SoftTrimRatio != 0.3 {
		t.Errorf("Expected SoftTrimRatio 0.3, got %f", config.SoftTrimRatio)
	}
	if config.HardClearRatio != 0.5 {
		t.Errorf("Expected HardClearRatio 0.5, got %f", config.HardClearRatio)
	}
	if config.KeepLastAssistants != 3 {
		t.Errorf("Expected KeepLastAssistants 3, got %d", config.KeepLastAssistants)
	}
	if config.MinPrunableChars != 50000 {
		t.Errorf("Expected MinPrunableChars 50000, got %d", config.MinPrunableChars)
	}
	if config.SoftTrimMaxChars != 4000 {
		t.Errorf("Expected SoftTrimMaxChars 4000, got %d", config.SoftTrimMaxChars)
	}
	if config.HardClearPlaceholder != "[Old tool result content cleared]" {
		t.Errorf("Unexpected placeholder: %s", config.HardClearPlaceholder)
	}
}
