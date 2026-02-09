package connector

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
)

// PruningConfig configures context pruning behavior (matches OpenClaw's AgentContextPruningConfig)
type PruningConfig struct {
	// Mode controls pruning strategy.
	// "off" disables proactive pruning.
	// "cache-ttl" enables proactive pruning using TTL-like refresh behavior.
	Mode string `yaml:"mode" json:"mode,omitempty"`

	// TTL is the refresh interval for cache-ttl mode.
	// Default: 1h
	TTL time.Duration `yaml:"ttl" json:"ttl,omitempty"`

	// Enabled turns on proactive context pruning
	Enabled bool `yaml:"enabled" json:"enabled"`

	// SoftTrimRatio is the context usage ratio (0.0-1.0) that triggers soft trimming
	// Default: 0.3 (30% of context window)
	SoftTrimRatio float64 `yaml:"soft_trim_ratio" json:"soft_trim_ratio,omitempty"`

	// HardClearRatio is the context usage ratio (0.0-1.0) that triggers hard clearing
	// Default: 0.5 (50% of context window)
	HardClearRatio float64 `yaml:"hard_clear_ratio" json:"hard_clear_ratio,omitempty"`

	// KeepLastAssistants protects the N most recent assistant messages from pruning
	// Default: 3
	KeepLastAssistants int `yaml:"keep_last_assistants" json:"keep_last_assistants,omitempty"`

	// MinPrunableChars is the minimum total chars in prunable tool results before hard clear kicks in
	// Default: 50000
	MinPrunableChars int `yaml:"min_prunable_chars" json:"min_prunable_chars,omitempty"`

	// SoftTrimMaxChars is the threshold for considering a tool result "large" (triggering soft trim)
	// Default: 4000
	SoftTrimMaxChars int `yaml:"soft_trim_max_chars" json:"soft_trim_max_chars,omitempty"`

	// SoftTrimHeadChars is how many chars to keep from the start when soft trimming
	// Default: 1500
	SoftTrimHeadChars int `yaml:"soft_trim_head_chars" json:"soft_trim_head_chars,omitempty"`

	// SoftTrimTailChars is how many chars to keep from the end when soft trimming
	// Default: 1500
	SoftTrimTailChars int `yaml:"soft_trim_tail_chars" json:"soft_trim_tail_chars,omitempty"`

	// HardClearEnabled allows disabling hard clear phase
	// Default: true
	HardClearEnabled *bool `yaml:"hard_clear_enabled" json:"hard_clear_enabled,omitempty"`

	// HardClearPlaceholder is the text that replaces cleared tool results
	// Default: "[Old tool result content cleared]"
	HardClearPlaceholder string `yaml:"hard_clear_placeholder" json:"hard_clear_placeholder,omitempty"`

	// ToolsAllow is a list of tool name patterns to prune (supports wildcards: list_*, *_search)
	// Empty means all tools are prunable (unless in deny list)
	ToolsAllow []string `yaml:"tools_allow" json:"tools_allow,omitempty"`

	// ToolsDeny is a list of tool name patterns to never prune (supports wildcards)
	ToolsDeny []string `yaml:"tools_deny" json:"tools_deny,omitempty"`

	// --- Compaction settings (LLM-based summarization) ---

	// SummarizationEnabled enables LLM-based summarization instead of placeholder text
	// Default: true (when compaction is enabled)
	SummarizationEnabled *bool `yaml:"summarization_enabled" json:"summarization_enabled,omitempty"`

	// SummarizationModel is the model to use for generating summaries
	// Default: "anthropic/claude-opus-4.6"
	SummarizationModel string `yaml:"summarization_model" json:"summarization_model,omitempty"`

	// MaxSummaryTokens is the maximum tokens for generated summaries
	// Default: 500
	MaxSummaryTokens int `yaml:"max_summary_tokens" json:"max_summary_tokens,omitempty"`

	// MaxHistoryShare is the maximum ratio of context that history can consume (0.0-1.0)
	// When exceeded, oldest messages are dropped and summarized
	// Default: 0.5 (50%)
	MaxHistoryShare float64 `yaml:"max_history_share" json:"max_history_share,omitempty"`

	// ReserveTokens is the token budget reserved for compaction output
	// Default: 2000
	ReserveTokens int `yaml:"reserve_tokens" json:"reserve_tokens,omitempty"`

	// CustomInstructions are additional instructions for the summarization model
	CustomInstructions string `yaml:"custom_instructions" json:"custom_instructions,omitempty"`

	// MemoryFlush runs a pre-compaction memory write pass.
	MemoryFlush *MemoryFlushConfig `yaml:"memory_flush" json:"memory_flush,omitempty"`

	// MaxHistoryTurns limits conversation history to the last N user turns (and their associated
	// assistant responses). This reduces token usage for long-running DM sessions.
	// A value of 0 means no limit (default behavior).
	// Default: 0 (unlimited)
	MaxHistoryTurns int `yaml:"max_history_turns" json:"max_history_turns,omitempty"`
}

// MemoryFlushConfig configures pre-compaction memory flush behavior (OpenClaw-style).
type MemoryFlushConfig struct {
	Enabled             *bool  `yaml:"enabled" json:"enabled,omitempty"`
	SoftThresholdTokens int    `yaml:"soft_threshold_tokens" json:"soft_threshold_tokens,omitempty"`
	Prompt              string `yaml:"prompt" json:"prompt,omitempty"`
	SystemPrompt        string `yaml:"system_prompt" json:"system_prompt,omitempty"`
}

// DefaultPruningConfig returns OpenClaw's default settings
func DefaultPruningConfig() *PruningConfig {
	enabled := true
	return &PruningConfig{
		Mode:                 "cache-ttl",
		TTL:                  1 * time.Hour,
		Enabled:              true,
		SoftTrimRatio:        0.3,
		HardClearRatio:       0.5,
		KeepLastAssistants:   3,
		MinPrunableChars:     50000,
		SoftTrimMaxChars:     4000,
		SoftTrimHeadChars:    1500,
		SoftTrimTailChars:    1500,
		HardClearEnabled:     &enabled,
		HardClearPlaceholder: "[Old tool result content cleared]",
	}
}

// Constants matching OpenClaw
const (
	// Approximate characters per token for estimation (OpenClaw uses 4)
	charsPerTokenEstimate = 4

	// Approximate chars for an image in context (OpenClaw uses 8000)
	imageCharEstimate = 8000
)

// messageInfo holds metadata about a message for pruning decisions
type messageInfo struct {
	index        int
	role         string
	charCount    int
	isToolCall   bool   // assistant message with tool calls
	isToolResult bool   // tool result message
	toolCallID   string // for tool results
	toolName     string // for tool results (used for allow/deny matching)
	hasImages    bool   // tool result contains images (never prune these)
}

// estimateChars estimates character count from a message (OpenClaw approach)
func estimateMessageChars(msg openai.ChatCompletionMessageParamUnion) int {
	switch {
	case msg.OfSystem != nil:
		return len(extractSystemContent(msg.OfSystem.Content))

	case msg.OfUser != nil:
		chars := len(extractUserContent(msg.OfUser.Content))
		// Add image estimates
		for _, part := range msg.OfUser.Content.OfArrayOfContentParts {
			if part.OfImageURL != nil {
				chars += imageCharEstimate
			}
		}
		return chars

	case msg.OfAssistant != nil:
		chars := len(extractAssistantContent(msg.OfAssistant.Content))
		// Add tool call arguments
		for _, tc := range msg.OfAssistant.ToolCalls {
			if tc.OfFunction != nil {
				chars += len(tc.OfFunction.Function.Name) + len(tc.OfFunction.Function.Arguments)
			}
		}
		return chars

	case msg.OfTool != nil:
		return len(extractToolContent(msg.OfTool.Content))
	}
	return 0
}

// analyzeMessage extracts metadata from a message for pruning decisions
func analyzeMessage(msg openai.ChatCompletionMessageParamUnion, index int) messageInfo {
	info := messageInfo{
		index:     index,
		charCount: estimateMessageChars(msg),
	}

	switch {
	case msg.OfSystem != nil:
		info.role = "system"

	case msg.OfUser != nil:
		info.role = "user"

	case msg.OfAssistant != nil:
		info.role = "assistant"
		info.isToolCall = len(msg.OfAssistant.ToolCalls) > 0

	case msg.OfTool != nil:
		info.role = "tool"
		info.isToolResult = true
		info.toolCallID = msg.OfTool.ToolCallID
	}

	return info
}

// compiledPattern represents a pre-compiled tool name pattern
type compiledPattern struct {
	kind  string // "all", "exact", or "regex"
	value string
	regex *regexp.Regexp
}

// compilePattern compiles a tool name pattern (supports wildcards like list_*, *_search)
func compilePattern(pattern string) compiledPattern {
	pattern = strings.TrimSpace(strings.ToLower(pattern))
	if pattern == "" {
		return compiledPattern{kind: "exact", value: ""}
	}
	if pattern == "*" {
		return compiledPattern{kind: "all"}
	}
	if !strings.Contains(pattern, "*") {
		return compiledPattern{kind: "exact", value: pattern}
	}
	// Convert glob pattern to regex
	escaped := regexp.QuoteMeta(pattern)
	rePattern := "^" + strings.ReplaceAll(escaped, "\\*", ".*") + "$"
	re, err := regexp.Compile(rePattern)
	if err != nil {
		return compiledPattern{kind: "exact", value: pattern}
	}
	return compiledPattern{kind: "regex", regex: re}
}

// matchesPattern checks if a tool name matches a compiled pattern
func matchesPattern(toolName string, p compiledPattern) bool {
	switch p.kind {
	case "all":
		return true
	case "exact":
		return toolName == p.value
	case "regex":
		return p.regex != nil && p.regex.MatchString(toolName)
	}
	return false
}

// matchesAnyPattern checks if a tool name matches any of the patterns
func matchesAnyPattern(toolName string, patterns []compiledPattern) bool {
	for _, p := range patterns {
		if matchesPattern(toolName, p) {
			return true
		}
	}
	return false
}

// makeToolPrunablePredicate creates a function that checks if a tool is prunable (OpenClaw pattern)
func makeToolPrunablePredicate(config *PruningConfig) func(toolName string) bool {
	if config == nil {
		return func(string) bool { return true }
	}

	var allowPatterns, denyPatterns []compiledPattern
	for _, p := range config.ToolsAllow {
		allowPatterns = append(allowPatterns, compilePattern(p))
	}
	for _, p := range config.ToolsDeny {
		denyPatterns = append(denyPatterns, compilePattern(p))
	}

	return func(toolName string) bool {
		normalized := strings.TrimSpace(strings.ToLower(toolName))
		// Check deny list first
		if matchesAnyPattern(normalized, denyPatterns) {
			return false
		}
		// If no allow list, everything not denied is allowed
		if len(allowPatterns) == 0 {
			return true
		}
		// Otherwise must be in allow list
		return matchesAnyPattern(normalized, allowPatterns)
	}
}

// softTrimToolResult truncates a large tool result keeping head and tail (OpenClaw style)
func softTrimToolResult(content string, config *PruningConfig) string {
	headChars := config.SoftTrimHeadChars
	tailChars := config.SoftTrimTailChars
	if headChars <= 0 {
		headChars = 1500
	}
	if tailChars <= 0 {
		tailChars = 1500
	}

	// Don't trim if content is small enough
	if len(content) <= headChars+tailChars+100 {
		return content
	}

	head := content[:headChars]
	tail := content[len(content)-tailChars:]

	return fmt.Sprintf("%s\n...\n%s\n\n[Tool result trimmed: kept first %d chars and last %d chars of %d chars.]",
		head, tail, headChars, tailChars, len(content))
}

// findAssistantCutoffIndex finds the index where protected tail starts (OpenClaw pattern)
// Messages at or after this index are protected from pruning
func findAssistantCutoffIndex(messages []messageInfo, keepLastAssistants int) int {
	if keepLastAssistants <= 0 {
		return len(messages) // Everything is potentially prunable
	}

	remaining := keepLastAssistants
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].role != "assistant" {
			continue
		}
		remaining--
		if remaining == 0 {
			return i
		}
	}

	// Not enough assistant messages - nothing is prunable
	return len(messages)
}

// findFirstUserIndex finds the index of the first user message (OpenClaw pattern)
// Never prune anything before this - protects bootstrap/identity files
func findFirstUserIndex(messages []messageInfo) int {
	for i, m := range messages {
		if m.role == "user" {
			return i
		}
	}
	return len(messages) // No user message found
}

// estimateTotalChars calculates total character count for messages
func estimateTotalChars(messages []messageInfo) int {
	total := 0
	for _, m := range messages {
		total += m.charCount
	}
	return total
}

// PruneContext prunes messages to fit within context window (OpenClaw algorithm)
// Phase 0: Limit history turns (if MaxHistoryTurns is set)
// Phase 1: Soft trim - truncate large tool results to head+tail
// Phase 2: Hard clear - replace old tool results with placeholder
func PruneContext(
	prompt []openai.ChatCompletionMessageParamUnion,
	config *PruningConfig,
	contextWindowTokens int,
) []openai.ChatCompletionMessageParamUnion {
	if config == nil || !config.Enabled {
		return prompt
	}
	if len(prompt) == 0 || contextWindowTokens <= 0 {
		return prompt
	}

	// Apply defaults for any missing config values
	cfg := applyPruningDefaults(config)

	// Phase 0: Limit history turns if configured
	// This is applied first to reduce the working set before other pruning phases
	if cfg.MaxHistoryTurns > 0 {
		prompt = LimitHistoryTurns(prompt, cfg.MaxHistoryTurns)
	}

	// Convert token window to char window (OpenClaw uses chars/4)
	charWindow := contextWindowTokens * charsPerTokenEstimate
	if charWindow <= 0 {
		return prompt
	}

	// Analyze all messages
	messages := make([]messageInfo, len(prompt))
	for i, msg := range prompt {
		messages[i] = analyzeMessage(msg, i)
	}

	// Find boundaries
	cutoffIndex := findAssistantCutoffIndex(messages, cfg.KeepLastAssistants)
	firstUserIndex := findFirstUserIndex(messages)
	pruneStartIndex := firstUserIndex // Never prune before first user message

	// Calculate current usage ratio
	totalChars := estimateTotalChars(messages)
	ratio := float64(totalChars) / float64(charWindow)

	// If under soft trim threshold, no pruning needed
	if ratio < cfg.SoftTrimRatio {
		return prompt
	}

	// Create predicate for tool pruning
	isToolPrunable := makeToolPrunablePredicate(cfg)

	// Find prunable tool result indices
	var prunableToolIndexes []int
	for i := pruneStartIndex; i < cutoffIndex; i++ {
		m := messages[i]
		if !m.isToolResult {
			continue
		}
		if m.hasImages {
			continue // Never prune tool results with images
		}
		if !isToolPrunable(m.toolName) {
			continue
		}
		prunableToolIndexes = append(prunableToolIndexes, i)
	}

	// Phase 1: Soft trim - truncate large tool results
	result := make([]openai.ChatCompletionMessageParamUnion, len(prompt))
	copy(result, prompt)

	for _, i := range prunableToolIndexes {
		msg := result[i]
		if msg.OfTool == nil {
			continue
		}
		content := extractToolContent(msg.OfTool.Content)
		if len(content) <= cfg.SoftTrimMaxChars {
			continue
		}

		// Soft trim this tool result
		trimmed := softTrimToolResult(content, cfg)
		result[i] = openai.ToolMessage(trimmed, msg.OfTool.ToolCallID)

		// Update char count
		oldChars := messages[i].charCount
		newChars := len(trimmed)
		totalChars += newChars - oldChars
		messages[i].charCount = newChars
	}

	// Recalculate ratio after soft trim
	ratio = float64(totalChars) / float64(charWindow)

	// If under hard clear threshold, we're done
	if ratio < cfg.HardClearRatio {
		return result
	}

	// Check if hard clear is enabled
	hardClearEnabled := cfg.HardClearEnabled == nil || *cfg.HardClearEnabled
	if !hardClearEnabled {
		return result
	}

	// Check if there's enough prunable content for hard clear
	var prunableToolChars int
	for _, i := range prunableToolIndexes {
		prunableToolChars += messages[i].charCount
	}
	if prunableToolChars < cfg.MinPrunableChars {
		return result
	}

	// Phase 2: Hard clear - replace old tool results with placeholder
	placeholder := cfg.HardClearPlaceholder
	if placeholder == "" {
		placeholder = "[Old tool result content cleared]"
	}

	for _, i := range prunableToolIndexes {
		if ratio < cfg.HardClearRatio {
			break
		}
		msg := result[i]
		if msg.OfTool == nil {
			continue
		}

		// Replace with placeholder
		result[i] = openai.ToolMessage(placeholder, msg.OfTool.ToolCallID)

		// Update totals
		oldChars := messages[i].charCount
		newChars := len(placeholder)
		totalChars += newChars - oldChars
		ratio = float64(totalChars) / float64(charWindow)
	}

	return result
}

// applyPruningDefaults fills in default values for any missing config fields
func applyPruningDefaults(config *PruningConfig) *PruningConfig {
	cfg := *config // Copy
	defaults := DefaultPruningConfig()

	if strings.TrimSpace(cfg.Mode) == "" {
		cfg.Mode = defaults.Mode
	}
	if cfg.TTL <= 0 {
		cfg.TTL = defaults.TTL
	}
	if cfg.SoftTrimRatio <= 0 {
		cfg.SoftTrimRatio = defaults.SoftTrimRatio
	}
	if cfg.HardClearRatio <= 0 {
		cfg.HardClearRatio = defaults.HardClearRatio
	}
	if cfg.KeepLastAssistants <= 0 {
		cfg.KeepLastAssistants = defaults.KeepLastAssistants
	}
	if cfg.MinPrunableChars <= 0 {
		cfg.MinPrunableChars = defaults.MinPrunableChars
	}
	if cfg.SoftTrimMaxChars <= 0 {
		cfg.SoftTrimMaxChars = defaults.SoftTrimMaxChars
	}
	if cfg.SoftTrimHeadChars <= 0 {
		cfg.SoftTrimHeadChars = defaults.SoftTrimHeadChars
	}
	if cfg.SoftTrimTailChars <= 0 {
		cfg.SoftTrimTailChars = defaults.SoftTrimTailChars
	}
	if cfg.HardClearPlaceholder == "" {
		cfg.HardClearPlaceholder = defaults.HardClearPlaceholder
	}

	return &cfg
}

// LimitHistoryTurns limits conversation history to the last N user turns (and their associated
// assistant responses and tool calls). This reduces token usage for long-running sessions.
// Returns the original prompt if limit is 0 or negative (unlimited).
func LimitHistoryTurns(
	prompt []openai.ChatCompletionMessageParamUnion,
	limit int,
) []openai.ChatCompletionMessageParamUnion {
	if limit <= 0 || len(prompt) == 0 {
		return prompt
	}

	// Find system message(s) at the start - these are always preserved
	systemEndIndex := 0
	for i, msg := range prompt {
		if msg.OfSystem != nil {
			systemEndIndex = i + 1
		} else {
			break
		}
	}

	// Count user turns from the end and find the cutoff point
	userCount := 0
	lastUserIndex := len(prompt)

	for i := len(prompt) - 1; i >= systemEndIndex; i-- {
		msg := prompt[i]
		if msg.OfUser != nil {
			userCount++
			if userCount > limit {
				// We've exceeded the limit, cut before this user turn's start
				// But we need to find where this turn's group starts
				// A "turn" includes the user message and any preceding tool results/calls
				// that are part of the same exchange
				result := make([]openai.ChatCompletionMessageParamUnion, 0, systemEndIndex+len(prompt)-lastUserIndex)
				// Add system messages
				result = append(result, prompt[:systemEndIndex]...)
				// Add messages from lastUserIndex onwards
				result = append(result, prompt[lastUserIndex:]...)
				return result
			}
			lastUserIndex = i
		}
	}

	// Didn't exceed limit, return original
	return prompt
}

// smartTruncatePrompt is the fallback for context_length errors (reactive pruning)
// Uses simple 50% reduction strategy
func smartTruncatePrompt(
	prompt []openai.ChatCompletionMessageParamUnion,
	targetReduction float64,
) []openai.ChatCompletionMessageParamUnion {
	if len(prompt) <= 2 {
		return nil
	}

	// Use PruneContext with aggressive settings for reactive pruning
	config := &PruningConfig{
		Enabled:            true,
		SoftTrimRatio:      0.0, // Always soft trim
		HardClearRatio:     0.0, // Always hard clear
		KeepLastAssistants: 2,
		MinPrunableChars:   0,
		SoftTrimMaxChars:   2000,
		SoftTrimHeadChars:  1000,
		SoftTrimTailChars:  500,
	}

	// Estimate a reasonable context window from message count
	// This is a fallback when we don't know the actual limit
	estimatedTokens := 0
	for _, msg := range prompt {
		estimatedTokens += estimateMessageChars(msg) / charsPerTokenEstimate
	}
	// Target reduction means we want to keep (1-targetReduction) of tokens
	targetTokens := int(float64(estimatedTokens) * (1 - targetReduction))
	if targetTokens < 1000 {
		targetTokens = 1000
	}

	result := PruneContext(prompt, config, targetTokens)
	if len(result) < 2 {
		return nil
	}
	return result
}
