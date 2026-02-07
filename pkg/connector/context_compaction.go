package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/rs/zerolog"
)

// CompactionConfig extends PruningConfig with summarization and event settings
type CompactionConfig struct {
	*PruningConfig

	// SummarizationEnabled enables LLM-based summarization instead of placeholder text
	// Default: true (when compaction is enabled)
	SummarizationEnabled *bool `yaml:"summarization_enabled" json:"summarization_enabled,omitempty"`

	// SummarizationModel is the model to use for generating summaries
	// Default: same as conversation model, or openai/gpt-5.2
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
}

// DefaultCompactionConfig returns default compaction settings
func DefaultCompactionConfig() *CompactionConfig {
	enabled := true
	return &CompactionConfig{
		PruningConfig:        DefaultPruningConfig(),
		SummarizationEnabled: &enabled,
		MaxSummaryTokens:     500,
		MaxHistoryShare:      0.5,
		ReserveTokens:        20000,
	}
}

// CompactionEventType represents the type of compaction event
type CompactionEventType string

const (
	CompactionEventStart CompactionEventType = "compaction_start"
	CompactionEventEnd   CompactionEventType = "compaction_end"
)

// CompactionEvent represents a compaction lifecycle event
type CompactionEvent struct {
	Type           CompactionEventType `json:"type"`
	SessionID      string              `json:"session_id,omitempty"`
	MessagesBefore int                 `json:"messages_before,omitempty"`
	MessagesAfter  int                 `json:"messages_after,omitempty"`
	TokensBefore   int                 `json:"tokens_before,omitempty"`
	TokensAfter    int                 `json:"tokens_after,omitempty"`
	Summary        string              `json:"summary,omitempty"`
	WillRetry      bool                `json:"will_retry,omitempty"`
	Error          string              `json:"error,omitempty"`
	Duration       time.Duration       `json:"duration_ms,omitempty"`
}

// CompactionHookContext provides context for compaction hooks
type CompactionHookContext struct {
	SessionID    string
	MessageCount int
	TokenCount   int
	Config       *CompactionConfig
}

// CompactionHookResult is returned by before_compaction hooks to modify behavior
type CompactionHookResult struct {
	// Skip if true, skips compaction entirely
	Skip bool
	// CustomSummary overrides the generated summary
	CustomSummary string
	// ModifiedConfig allows hooks to modify compaction config
	ModifiedConfig *CompactionConfig
}

// CompactionBeforeHook is called before compaction starts
type CompactionBeforeHook func(ctx context.Context, hookCtx *CompactionHookContext) (*CompactionHookResult, error)

// CompactionAfterHook is called after compaction completes
type CompactionAfterHook func(ctx context.Context, event *CompactionEvent) error

// CompactionHooks manages registered compaction hooks
type CompactionHooks struct {
	mu          sync.RWMutex
	beforeHooks []CompactionBeforeHook
	afterHooks  []CompactionAfterHook
}

// globalCompactionHooks is the global hook registry
var globalCompactionHooks = &CompactionHooks{}

// RegisterBeforeCompactionHook registers a hook to run before compaction
func RegisterBeforeCompactionHook(hook CompactionBeforeHook) {
	globalCompactionHooks.mu.Lock()
	defer globalCompactionHooks.mu.Unlock()
	globalCompactionHooks.beforeHooks = append(globalCompactionHooks.beforeHooks, hook)
}

// RegisterAfterCompactionHook registers a hook to run after compaction
func RegisterAfterCompactionHook(hook CompactionAfterHook) {
	globalCompactionHooks.mu.Lock()
	defer globalCompactionHooks.mu.Unlock()
	globalCompactionHooks.afterHooks = append(globalCompactionHooks.afterHooks, hook)
}

// runBeforeHooks runs all registered before hooks
func (h *CompactionHooks) runBeforeHooks(ctx context.Context, hookCtx *CompactionHookContext) (*CompactionHookResult, error) {
	h.mu.RLock()
	hooks := make([]CompactionBeforeHook, len(h.beforeHooks))
	copy(hooks, h.beforeHooks)
	h.mu.RUnlock()

	for _, hook := range hooks {
		result, err := hook(ctx, hookCtx)
		if err != nil {
			return nil, err
		}
		if result != nil && (result.Skip || result.CustomSummary != "" || result.ModifiedConfig != nil) {
			return result, nil
		}
	}
	return nil, nil
}

// runAfterHooks runs all registered after hooks
func (h *CompactionHooks) runAfterHooks(ctx context.Context, event *CompactionEvent) {
	h.mu.RLock()
	hooks := make([]CompactionAfterHook, len(h.afterHooks))
	copy(hooks, h.afterHooks)
	h.mu.RUnlock()

	for _, hook := range hooks {
		if err := hook(ctx, event); err != nil {
			zerolog.Ctx(ctx).Warn().Err(err).Msg("Compaction after hook failed")
		}
	}
}

// CompactionEventEmitter handles emitting compaction events to clients
type CompactionEventEmitter func(ctx context.Context, event *CompactionEvent)

// CompactionResult holds the result of a compaction operation
type CompactionResult struct {
	Compacted       bool
	Summary         string
	MessagesBefore  int
	MessagesAfter   int
	TokensBefore    int
	TokensAfter     int
	DroppedMessages int
	Error           error
}

// Compactor handles context compaction with LLM summarization
type Compactor struct {
	api    *openai.Client
	log    zerolog.Logger
	config *CompactionConfig

	// Event emitter for client notifications
	eventEmitter CompactionEventEmitter

	// Model to use for summarization
	summarizationModel string
}

// NewCompactor creates a new compactor instance
func NewCompactor(api *openai.Client, log zerolog.Logger, config *CompactionConfig) *Compactor {
	if config == nil {
		config = DefaultCompactionConfig()
	}
	return &Compactor{
		api:    api,
		log:    log,
		config: config,
	}
}

// SetEventEmitter sets the event emitter for compaction events
func (c *Compactor) SetEventEmitter(emitter CompactionEventEmitter) {
	c.eventEmitter = emitter
}

// SetSummarizationModel sets the model used for generating summaries
func (c *Compactor) SetSummarizationModel(model string) {
	c.summarizationModel = model
}

// emitEvent emits a compaction event if an emitter is configured
func (c *Compactor) emitEvent(ctx context.Context, event *CompactionEvent) {
	if c.eventEmitter != nil {
		c.eventEmitter(ctx, event)
	}
}

// CompactContext performs intelligent context compaction with LLM summarization
func (c *Compactor) CompactContext(
	ctx context.Context,
	sessionID string,
	messages []openai.ChatCompletionMessageParamUnion,
	contextWindowTokens int,
) (*CompactionResult, []openai.ChatCompletionMessageParamUnion) {
	startTime := time.Now()
	result := &CompactionResult{
		MessagesBefore: len(messages),
	}

	// Estimate initial tokens
	result.TokensBefore = c.estimateTotalTokens(messages)

	// Run before hooks
	hookCtx := &CompactionHookContext{
		SessionID:    sessionID,
		MessageCount: len(messages),
		TokenCount:   result.TokensBefore,
		Config:       c.config,
	}

	// Emit start event
	c.emitEvent(ctx, &CompactionEvent{
		Type:           CompactionEventStart,
		SessionID:      sessionID,
		MessagesBefore: result.MessagesBefore,
		TokensBefore:   result.TokensBefore,
	})

	hookResult, err := globalCompactionHooks.runBeforeHooks(ctx, hookCtx)
	if err != nil {
		c.log.Warn().Err(err).Msg("Compaction before hook failed")
	}
	if hookResult != nil && hookResult.Skip {
		result.MessagesAfter = len(messages)
		result.TokensAfter = result.TokensBefore
		return result, messages
	}
	if hookResult != nil && hookResult.ModifiedConfig != nil {
		c.config = hookResult.ModifiedConfig
	}

	// Check if compaction is needed based on history share
	charWindow := contextWindowTokens * charsPerTokenEstimate
	totalChars := 0
	for _, msg := range messages {
		totalChars += estimateMessageChars(msg)
	}

	historyRatio := float64(totalChars) / float64(charWindow)
	maxHistoryShare := c.config.MaxHistoryShare
	if maxHistoryShare <= 0 {
		maxHistoryShare = 0.5
	}

	// If under threshold, just apply standard pruning
	if historyRatio < maxHistoryShare {
		pruned := PruneContext(messages, c.config.PruningConfig, contextWindowTokens)
		result.MessagesAfter = len(pruned)
		result.TokensAfter = c.estimateTotalTokens(pruned)
		result.Compacted = result.MessagesAfter < result.MessagesBefore

		c.emitCompactionEnd(ctx, sessionID, result, startTime, false, "")
		return result, pruned
	}

	// Compaction is needed - identify messages to summarize
	messagesToSummarize, messagesToKeep := c.splitMessagesForCompaction(messages, contextWindowTokens)

	if len(messagesToSummarize) == 0 {
		// Nothing to summarize, apply standard pruning
		pruned := PruneContext(messages, c.config.PruningConfig, contextWindowTokens)
		result.MessagesAfter = len(pruned)
		result.TokensAfter = c.estimateTotalTokens(pruned)
		result.Compacted = result.MessagesAfter < result.MessagesBefore

		c.emitCompactionEnd(ctx, sessionID, result, startTime, false, "")
		return result, pruned
	}

	// Check if hook provided a custom summary
	var summary string
	if hookResult != nil && hookResult.CustomSummary != "" {
		summary = hookResult.CustomSummary
	} else {
		// Generate LLM summary
		summaryEnabled := c.config.SummarizationEnabled == nil || *c.config.SummarizationEnabled
		if summaryEnabled && c.api != nil {
			var summaryErr error
			summary, summaryErr = c.generateSummary(ctx, messagesToSummarize)
			if summaryErr != nil {
				c.log.Warn().Err(summaryErr).Msg("Failed to generate compaction summary, using fallback")
				summary = c.generateFallbackSummary(messagesToSummarize)
			}
		} else {
			summary = c.generateFallbackSummary(messagesToSummarize)
		}
	}

	result.Summary = summary
	result.DroppedMessages = len(messagesToSummarize)

	// Build compacted messages: system prompt + summary + kept messages
	compactedMessages := c.buildCompactedMessages(messages, messagesToKeep, summary)

	// Apply standard pruning to the compacted result
	pruned := PruneContext(compactedMessages, c.config.PruningConfig, contextWindowTokens)

	result.MessagesAfter = len(pruned)
	result.TokensAfter = c.estimateTotalTokens(pruned)
	result.Compacted = true

	c.emitCompactionEnd(ctx, sessionID, result, startTime, false, "")
	return result, pruned
}

// emitCompactionEnd emits the end event and runs after hooks
func (c *Compactor) emitCompactionEnd(ctx context.Context, sessionID string, result *CompactionResult, startTime time.Time, willRetry bool, errMsg string) {
	event := &CompactionEvent{
		Type:           CompactionEventEnd,
		SessionID:      sessionID,
		MessagesBefore: result.MessagesBefore,
		MessagesAfter:  result.MessagesAfter,
		TokensBefore:   result.TokensBefore,
		TokensAfter:    result.TokensAfter,
		Summary:        result.Summary,
		WillRetry:      willRetry,
		Error:          errMsg,
		Duration:       time.Since(startTime),
	}

	c.emitEvent(ctx, event)
	globalCompactionHooks.runAfterHooks(ctx, event)
}

// splitMessagesForCompaction splits messages into those to summarize and those to keep
func (c *Compactor) splitMessagesForCompaction(
	messages []openai.ChatCompletionMessageParamUnion,
	contextWindowTokens int,
) (toSummarize, toKeep []openai.ChatCompletionMessageParamUnion) {
	if len(messages) <= 2 {
		return nil, messages
	}

	// Analyze messages
	infos := make([]messageInfo, len(messages))
	for i, msg := range messages {
		infos[i] = analyzeMessage(msg, i)
	}

	// Find boundaries
	keepLastAssistants := c.config.KeepLastAssistants
	if keepLastAssistants <= 0 {
		keepLastAssistants = 3
	}
	cutoffIndex := findAssistantCutoffIndex(infos, keepLastAssistants)
	firstUserIndex := findFirstUserIndex(infos)

	// Calculate how much we need to drop to fit in history budget
	charWindow := contextWindowTokens * charsPerTokenEstimate
	maxHistoryChars := int(float64(charWindow) * c.config.MaxHistoryShare)

	totalChars := 0
	for _, info := range infos {
		totalChars += info.charCount
	}

	// If we're over budget, find where to split
	if totalChars <= maxHistoryChars {
		return nil, messages
	}

	// We need to drop from firstUserIndex up to some point before cutoffIndex
	charsToRemove := totalChars - maxHistoryChars
	removedChars := 0
	splitIndex := firstUserIndex

	for i := firstUserIndex; i < cutoffIndex && removedChars < charsToRemove; i++ {
		removedChars += infos[i].charCount
		splitIndex = i + 1
	}

	// Never summarize the system prompt (index 0 if present)
	systemPrompt := -1
	if len(messages) > 0 && messages[0].OfSystem != nil {
		systemPrompt = 0
	}

	// Build the lists
	for i, msg := range messages {
		if i == systemPrompt {
			// System prompt goes to keep
			toKeep = append(toKeep, msg)
		} else if i < splitIndex {
			toSummarize = append(toSummarize, msg)
		} else {
			toKeep = append(toKeep, msg)
		}
	}

	return toSummarize, toKeep
}

// generateSummary uses the LLM to generate a summary of messages
func (c *Compactor) generateSummary(ctx context.Context, messages []openai.ChatCompletionMessageParamUnion) (string, error) {
	if len(messages) == 0 {
		return "No prior history.", nil
	}

	// Build conversation text for summarization
	var conversation strings.Builder
	for _, msg := range messages {
		switch {
		case msg.OfUser != nil:
			conversation.WriteString("User: ")
			conversation.WriteString(extractUserContent(msg.OfUser.Content))
			conversation.WriteString("\n\n")
		case msg.OfAssistant != nil:
			conversation.WriteString("Assistant: ")
			conversation.WriteString(extractAssistantContent(msg.OfAssistant.Content))
			conversation.WriteString("\n\n")
		case msg.OfTool != nil:
			content := extractToolContent(msg.OfTool.Content)
			// Truncate long tool results for summary input
			if len(content) > 1000 {
				content = content[:500] + "\n...[truncated]...\n" + content[len(content)-500:]
			}
			conversation.WriteString("Tool Result: ")
			conversation.WriteString(content)
			conversation.WriteString("\n\n")
		}
	}

	// Build summarization prompt
	systemPrompt := `You are a context summarizer. Your task is to create a concise summary of the conversation that preserves:
1. Key decisions made
2. Important facts and constraints discovered
3. TODOs and open questions
4. File paths and code references mentioned
5. Tool failures and their outcomes

Be concise but preserve critical context. Format the summary as bullet points.`

	if c.config.CustomInstructions != "" {
		systemPrompt += "\n\nAdditional instructions: " + c.config.CustomInstructions
	}

	userPrompt := fmt.Sprintf("Summarize this conversation history:\n\n%s", conversation.String())

	// Use configured model or default
	model := c.summarizationModel
	if model == "" {
		model = c.config.SummarizationModel
	}
	if model == "" {
		model = "openai/gpt-5.2"
	}

	maxTokens := c.config.MaxSummaryTokens
	if maxTokens <= 0 {
		maxTokens = 500
	}

	params := responses.ResponseNewParams{
		Model:           shared.ResponsesModel(model),
		Instructions:    openai.String(systemPrompt),
		MaxOutputTokens: openai.Int(int64(maxTokens)),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(userPrompt),
		},
	}

	// Disable reasoning for summarization
	params.Reasoning = shared.ReasoningParam{
		Effort: shared.ReasoningEffortNone,
	}

	resp, err := c.api.Responses.New(ctx, params)
	if err != nil {
		// Retry without reasoning param if it failed
		params.Reasoning = shared.ReasoningParam{}
		resp, err = c.api.Responses.New(ctx, params)
		if err != nil {
			return "", fmt.Errorf("summarization API call failed: %w", err)
		}
	}

	// Extract summary from response
	summary := extractSummaryFromResponse(resp)
	if summary == "" {
		return "", errors.New("summarization returned empty response")
	}

	return summary, nil
}

// extractSummaryFromResponse extracts text content from a responses.Response
func extractSummaryFromResponse(resp *responses.Response) string {
	var content strings.Builder

	for _, item := range resp.Output {
		switch item := item.AsAny().(type) {
		case responses.ResponseOutputMessage:
			for _, part := range item.Content {
				if part.Type == "output_text" || part.Type == "text" {
					if part.Text != "" {
						content.WriteString(part.Text)
					}
				}
			}
		}
	}

	return strings.TrimSpace(content.String())
}

// generateFallbackSummary creates a basic summary without LLM when summarization fails
func (c *Compactor) generateFallbackSummary(messages []openai.ChatCompletionMessageParamUnion) string {
	if len(messages) == 0 {
		return "No prior history."
	}

	// Collect basic stats
	userMsgs := 0
	assistantMsgs := 0
	toolResults := 0
	var topics []string

	for _, msg := range messages {
		switch {
		case msg.OfUser != nil:
			userMsgs++
			// Extract first few words as potential topic
			content := extractUserContent(msg.OfUser.Content)
			if len(content) > 100 {
				content = content[:100]
			}
			if content != "" && len(topics) < 3 {
				topics = append(topics, strings.Split(content, "\n")[0])
			}
		case msg.OfAssistant != nil:
			assistantMsgs++
		case msg.OfTool != nil:
			toolResults++
		}
	}

	var summary strings.Builder
	summary.WriteString(fmt.Sprintf("[Compacted: %d user messages, %d assistant responses, %d tool results]\n\n",
		userMsgs, assistantMsgs, toolResults))

	if len(topics) > 0 {
		summary.WriteString("Topics discussed:\n")
		for _, topic := range topics {
			if len(topic) > 80 {
				topic = topic[:80] + "..."
			}
			summary.WriteString(fmt.Sprintf("- %s\n", topic))
		}
	}

	return summary.String()
}

// buildCompactedMessages builds the final message list with summary injected
func (c *Compactor) buildCompactedMessages(
	original []openai.ChatCompletionMessageParamUnion,
	kept []openai.ChatCompletionMessageParamUnion,
	summary string,
) []openai.ChatCompletionMessageParamUnion {
	var result []openai.ChatCompletionMessageParamUnion

	// Find and add system prompt if present
	if len(original) > 0 && original[0].OfSystem != nil {
		result = append(result, original[0])
	}

	// Add summary as a user message with context marker
	if summary != "" {
		summaryMessage := openai.UserMessage(fmt.Sprintf(
			"[CONTEXT SUMMARY - Previous conversation was compacted]\n\n%s\n\n[END CONTEXT SUMMARY]",
			summary,
		))
		result = append(result, summaryMessage)
	}

	// Add kept messages (excluding system prompt if already added)
	for _, msg := range kept {
		if msg.OfSystem != nil {
			continue // Skip system prompts in kept (already added)
		}
		result = append(result, msg)
	}

	return result
}

// estimateTotalTokens estimates token count for a message list
func (c *Compactor) estimateTotalTokens(messages []openai.ChatCompletionMessageParamUnion) int {
	totalChars := 0
	for _, msg := range messages {
		totalChars += estimateMessageChars(msg)
	}
	return totalChars / charsPerTokenEstimate
}

// CompactOnOverflow performs compaction when a context length error is detected
// This is called before reactive truncation to try to preserve more context
func (c *Compactor) CompactOnOverflow(
	ctx context.Context,
	sessionID string,
	messages []openai.ChatCompletionMessageParamUnion,
	contextWindowTokens int,
	requestedTokens int,
) (*CompactionResult, []openai.ChatCompletionMessageParamUnion, bool) {
	c.log.Info().
		Int("messages", len(messages)).
		Int("context_window", contextWindowTokens).
		Int("requested_tokens", requestedTokens).
		Msg("Attempting auto-compaction on context overflow")

	// Temporarily enable aggressive settings for overflow recovery
	originalConfig := c.config
	c.config = &CompactionConfig{
		PruningConfig: &PruningConfig{
			Enabled:              true,
			SoftTrimRatio:        0.0, // Always soft trim
			HardClearRatio:       0.0, // Always hard clear
			KeepLastAssistants:   2,
			MinPrunableChars:     0,
			SoftTrimMaxChars:     2000,
			SoftTrimHeadChars:    1000,
			SoftTrimTailChars:    500,
			HardClearEnabled:     originalConfig.PruningConfig.HardClearEnabled,
			HardClearPlaceholder: originalConfig.PruningConfig.HardClearPlaceholder,
		},
		SummarizationEnabled: originalConfig.SummarizationEnabled,
		SummarizationModel:   originalConfig.SummarizationModel,
		MaxSummaryTokens:     originalConfig.MaxSummaryTokens,
		MaxHistoryShare:      0.3, // More aggressive for overflow
		ReserveTokens:        originalConfig.ReserveTokens,
		CustomInstructions:   originalConfig.CustomInstructions,
	}
	defer func() { c.config = originalConfig }()

	result, compacted := c.CompactContext(ctx, sessionID, messages, contextWindowTokens)

	if !result.Compacted || result.TokensAfter >= requestedTokens {
		c.log.Warn().
			Int("tokens_after", result.TokensAfter).
			Int("requested", requestedTokens).
			Msg("Auto-compaction did not reduce context enough")
		return result, messages, false
	}

	c.log.Info().
		Int("tokens_before", result.TokensBefore).
		Int("tokens_after", result.TokensAfter).
		Int("messages_before", result.MessagesBefore).
		Int("messages_after", result.MessagesAfter).
		Msg("Auto-compaction succeeded")

	return result, compacted, true
}
