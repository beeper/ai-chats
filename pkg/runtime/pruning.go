package runtime

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
)

// PruningConfig configures context pruning behavior (OpenClaw-style).
type PruningConfig struct {
	Mode string        `yaml:"mode" json:"mode,omitempty"`
	TTL  time.Duration `yaml:"ttl" json:"ttl,omitempty"`

	Enabled bool `yaml:"enabled" json:"enabled"`

	SoftTrimRatio  float64 `yaml:"soft_trim_ratio" json:"soft_trim_ratio,omitempty"`
	HardClearRatio float64 `yaml:"hard_clear_ratio" json:"hard_clear_ratio,omitempty"`

	KeepLastAssistants int `yaml:"keep_last_assistants" json:"keep_last_assistants,omitempty"`
	MinPrunableChars   int `yaml:"min_prunable_chars" json:"min_prunable_chars,omitempty"`
	SoftTrimMaxChars   int `yaml:"soft_trim_max_chars" json:"soft_trim_max_chars,omitempty"`
	SoftTrimHeadChars  int `yaml:"soft_trim_head_chars" json:"soft_trim_head_chars,omitempty"`
	SoftTrimTailChars  int `yaml:"soft_trim_tail_chars" json:"soft_trim_tail_chars,omitempty"`

	HardClearEnabled     *bool  `yaml:"hard_clear_enabled" json:"hard_clear_enabled,omitempty"`
	HardClearPlaceholder string `yaml:"hard_clear_placeholder" json:"hard_clear_placeholder,omitempty"`

	ToolsAllow []string `yaml:"tools_allow" json:"tools_allow,omitempty"`
	ToolsDeny  []string `yaml:"tools_deny" json:"tools_deny,omitempty"`

	SummarizationEnabled   *bool                `yaml:"summarization_enabled" json:"summarization_enabled,omitempty"`
	SummarizationModel     string               `yaml:"summarization_model" json:"summarization_model,omitempty"`
	MaxSummaryTokens       int                  `yaml:"max_summary_tokens" json:"max_summary_tokens,omitempty"`
	MaxHistoryShare        float64              `yaml:"max_history_share" json:"max_history_share,omitempty"`
	ReserveTokens          int                  `yaml:"reserve_tokens" json:"reserve_tokens,omitempty"`
	CustomInstructions     string               `yaml:"custom_instructions" json:"custom_instructions,omitempty"`
	IdentifierPolicy       string               `yaml:"identifier_policy" json:"identifier_policy,omitempty"`
	IdentifierInstructions string               `yaml:"identifier_instructions" json:"identifier_instructions,omitempty"`
	OverflowFlush          *OverflowFlushConfig `yaml:"overflow_flush" json:"overflow_flush,omitempty"`

	MaxHistoryTurns int `yaml:"max_history_turns" json:"max_history_turns,omitempty"`
}

// OverflowFlushConfig configures pre-compaction flush behavior.
type OverflowFlushConfig struct {
	Enabled             *bool  `yaml:"enabled" json:"enabled,omitempty"`
	SoftThresholdTokens int    `yaml:"soft_threshold_tokens" json:"soft_threshold_tokens,omitempty"`
	Prompt              string `yaml:"prompt" json:"prompt,omitempty"`
	SystemPrompt        string `yaml:"system_prompt" json:"system_prompt,omitempty"`
}

// DefaultPruningConfig returns OpenClaw-like default settings.
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
		OverflowFlush: &OverflowFlushConfig{
			Enabled:             &enabled,
			SoftThresholdTokens: 4000,
			Prompt:              "Pre-compaction overflow flush. Persist any durable notes now if your tools support it. If nothing to store, reply with NO_REPLY.",
			SystemPrompt:        "Pre-compaction overflow flush turn. The session is near auto-compaction; persist durable notes if possible. You may reply, but usually NO_REPLY is correct.",
		},
	}
}

type pruningMessageInfo struct {
	role         string
	charCount    int
	isToolResult bool
	toolName     string
	hasImages    bool
}

func analyzePruningMessage(msg openai.ChatCompletionMessageParamUnion) pruningMessageInfo {
	info := pruningMessageInfo{charCount: EstimateMessageChars(msg)}
	switch {
	case msg.OfSystem != nil:
		info.role = "system"
	case msg.OfUser != nil:
		info.role = "user"
		for _, part := range msg.OfUser.Content.OfArrayOfContentParts {
			if part.OfImageURL != nil {
				info.hasImages = true
				break
			}
		}
	case msg.OfAssistant != nil:
		info.role = "assistant"
	case msg.OfTool != nil:
		info.role = "tool"
		info.isToolResult = true
	}
	return info
}

type compiledPattern struct {
	kind  string
	value string
	regex *regexp.Regexp
}

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
	escaped := regexp.QuoteMeta(pattern)
	rePattern := "^" + strings.ReplaceAll(escaped, "\\*", ".*") + "$"
	re, err := regexp.Compile(rePattern)
	if err != nil {
		return compiledPattern{kind: "exact", value: pattern}
	}
	return compiledPattern{kind: "regex", regex: re}
}

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

func matchesAnyPattern(toolName string, patterns []compiledPattern) bool {
	for _, p := range patterns {
		if matchesPattern(toolName, p) {
			return true
		}
	}
	return false
}

// BuildToolPrunablePredicate creates a predicate for tool pruning allow/deny lists.
func BuildToolPrunablePredicate(config *PruningConfig) func(toolName string) bool {
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
		if matchesAnyPattern(normalized, denyPatterns) {
			return false
		}
		if len(allowPatterns) == 0 {
			return true
		}
		return matchesAnyPattern(normalized, allowPatterns)
	}
}

// SoftTrimToolResult truncates a large tool result while preserving head/tail context.
func SoftTrimToolResult(content string, config *PruningConfig) string {
	headChars := config.SoftTrimHeadChars
	tailChars := config.SoftTrimTailChars
	if headChars <= 0 {
		headChars = 1500
	}
	if tailChars <= 0 {
		tailChars = 1500
	}
	if len(content) <= headChars+tailChars+100 {
		return content
	}
	head := content[:headChars]
	tail := content[len(content)-tailChars:]
	return fmt.Sprintf("%s\n...\n%s\n\n[Tool result trimmed: kept first %d chars and last %d chars of %d chars.]",
		head, tail, headChars, tailChars, len(content))
}

func findAssistantCutoffIndex(messages []pruningMessageInfo, keepLastAssistants int) int {
	if keepLastAssistants <= 0 {
		return len(messages)
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
	return len(messages)
}

func findFirstUserIndex(messages []pruningMessageInfo) int {
	for i, m := range messages {
		if m.role == "user" {
			return i
		}
	}
	return len(messages)
}

func estimateTotalChars(messages []pruningMessageInfo) int {
	total := 0
	for _, m := range messages {
		total += m.charCount
	}
	return total
}

// ApplyPruningDefaults fills in missing pruning config values.
func ApplyPruningDefaults(config *PruningConfig) *PruningConfig {
	cfg := *config
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
	if cfg.OverflowFlush == nil {
		cfg.OverflowFlush = defaults.OverflowFlush
	}
	return &cfg
}

// LimitHistoryTurns keeps only the last N user turns plus required preamble.
func LimitHistoryTurns(prompt []openai.ChatCompletionMessageParamUnion, limit int) []openai.ChatCompletionMessageParamUnion {
	if limit <= 0 || len(prompt) == 0 {
		return prompt
	}

	systemEndIndex := 0
	for i, msg := range prompt {
		if msg.OfSystem != nil {
			systemEndIndex = i + 1
		} else {
			break
		}
	}

	userCount := 0
	lastUserIndex := len(prompt)
	for i := len(prompt) - 1; i >= systemEndIndex; i-- {
		if prompt[i].OfUser != nil {
			userCount++
			if userCount > limit {
				result := make([]openai.ChatCompletionMessageParamUnion, 0, systemEndIndex+len(prompt)-lastUserIndex)
				result = append(result, prompt[:systemEndIndex]...)
				result = append(result, prompt[lastUserIndex:]...)
				return result
			}
			lastUserIndex = i
		}
	}
	return prompt
}

// PruneContext applies proactive context pruning.
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

	cfg := ApplyPruningDefaults(config)
	if cfg.MaxHistoryTurns > 0 {
		prompt = LimitHistoryTurns(prompt, cfg.MaxHistoryTurns)
	}

	charWindow := contextWindowTokens * CharsPerTokenEstimate
	if charWindow <= 0 {
		return prompt
	}

	messages := make([]pruningMessageInfo, len(prompt))
	for i, msg := range prompt {
		messages[i] = analyzePruningMessage(msg)
	}

	cutoffIndex := findAssistantCutoffIndex(messages, cfg.KeepLastAssistants)
	pruneStartIndex := findFirstUserIndex(messages)
	totalChars := estimateTotalChars(messages)
	ratio := float64(totalChars) / float64(charWindow)
	if ratio < cfg.SoftTrimRatio {
		return prompt
	}

	isToolPrunable := BuildToolPrunablePredicate(cfg)
	var prunableToolIndexes []int
	for i := pruneStartIndex; i < cutoffIndex; i++ {
		m := messages[i]
		if !m.isToolResult || m.hasImages || !isToolPrunable(m.toolName) {
			continue
		}
		prunableToolIndexes = append(prunableToolIndexes, i)
	}

	result := make([]openai.ChatCompletionMessageParamUnion, len(prompt))
	copy(result, prompt)

	for _, i := range prunableToolIndexes {
		msg := result[i]
		if msg.OfTool == nil {
			continue
		}
		content := ExtractToolContent(msg.OfTool.Content)
		if len(content) <= cfg.SoftTrimMaxChars {
			continue
		}
		trimmed := SoftTrimToolResult(content, cfg)
		result[i] = openai.ToolMessage(trimmed, msg.OfTool.ToolCallID)
		oldChars := messages[i].charCount
		newChars := len(trimmed)
		totalChars += newChars - oldChars
		messages[i].charCount = newChars
	}

	ratio = float64(totalChars) / float64(charWindow)
	if ratio < cfg.HardClearRatio {
		return result
	}

	hardClearEnabled := cfg.HardClearEnabled == nil || *cfg.HardClearEnabled
	if !hardClearEnabled {
		return result
	}

	prunableToolChars := 0
	for _, i := range prunableToolIndexes {
		prunableToolChars += messages[i].charCount
	}
	if prunableToolChars < cfg.MinPrunableChars {
		return result
	}

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
		result[i] = openai.ToolMessage(placeholder, msg.OfTool.ToolCallID)
		oldChars := messages[i].charCount
		newChars := len(placeholder)
		totalChars += newChars - oldChars
		ratio = float64(totalChars) / float64(charWindow)
	}
	return result
}

// SmartTruncatePrompt is the reactive fallback for context overflow retries.
func SmartTruncatePrompt(prompt []openai.ChatCompletionMessageParamUnion, targetReduction float64) []openai.ChatCompletionMessageParamUnion {
	if len(prompt) <= 2 {
		return nil
	}

	config := &PruningConfig{
		Enabled:            true,
		SoftTrimRatio:      0.0,
		HardClearRatio:     0.0,
		KeepLastAssistants: 2,
		MinPrunableChars:   0,
		SoftTrimMaxChars:   2000,
		SoftTrimHeadChars:  1000,
		SoftTrimTailChars:  500,
	}

	estimatedTokens := 0
	for _, msg := range prompt {
		estimatedTokens += EstimateMessageChars(msg) / CharsPerTokenEstimate
	}
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
