package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/agents"
)

const (
	defaultMemoryFlushSoftTokens = 4000
)

var (
	defaultMemoryFlushPrompt = strings.Join([]string{
		"Pre-compaction memory flush.",
		"Store durable memories now (use memory/YYYY-MM-DD.md; create memory/ if needed).",
		"If nothing to store, reply with " + agents.SilentReplyToken + ".",
	}, " ")
	defaultMemoryFlushSystemPrompt = strings.Join([]string{
		"Pre-compaction memory flush turn.",
		"The session is near auto-compaction; capture durable memories to disk.",
		"You may reply, but usually " + agents.SilentReplyToken + " is correct.",
	}, " ")
)

type memoryFlushSettings struct {
	softThresholdTokens int
	prompt              string
	systemPrompt        string
}

func resolveMemoryFlushSettings(config *CompactionConfig) *memoryFlushSettings {
	if config == nil || config.PruningConfig == nil {
		return &memoryFlushSettings{
			softThresholdTokens: defaultMemoryFlushSoftTokens,
			prompt:              defaultMemoryFlushPrompt,
			systemPrompt:        defaultMemoryFlushSystemPrompt,
		}
	}
	cfg := config.PruningConfig.MemoryFlush
	if cfg != nil && cfg.Enabled != nil && !*cfg.Enabled {
		return nil
	}
	soft := defaultMemoryFlushSoftTokens
	if cfg != nil && cfg.SoftThresholdTokens > 0 {
		soft = cfg.SoftThresholdTokens
	}
	prompt := defaultMemoryFlushPrompt
	if cfg != nil && strings.TrimSpace(cfg.Prompt) != "" {
		prompt = cfg.Prompt
	}
	systemPrompt := defaultMemoryFlushSystemPrompt
	if cfg != nil && strings.TrimSpace(cfg.SystemPrompt) != "" {
		systemPrompt = cfg.SystemPrompt
	}
	return &memoryFlushSettings{
		softThresholdTokens: soft,
		prompt:              ensureNoReplyHint(prompt),
		systemPrompt:        ensureNoReplyHint(systemPrompt),
	}
}

func ensureNoReplyHint(text string) string {
	if strings.Contains(text, agents.SilentReplyToken) {
		return text
	}
	return text + "\n\nIf no user-visible reply is needed, start with " + agents.SilentReplyToken + "."
}

func (oc *AIClient) shouldRunMemoryFlush(meta *PortalMetadata, totalTokens, contextWindow, reserveTokens int, settings *memoryFlushSettings) bool {
	if settings == nil {
		return false
	}
	if totalTokens <= 0 || contextWindow <= 0 {
		return false
	}
	threshold := contextWindow - reserveTokens - settings.softThresholdTokens
	if threshold <= 0 || totalTokens < threshold {
		return false
	}
	if meta != nil && meta.MemoryFlushAt > 0 && meta.MemoryFlushCompactionCount == meta.CompactionCount {
		return false
	}
	return true
}

func (oc *AIClient) maybeRunMemoryFlush(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt []openai.ChatCompletionMessageParamUnion,
) {
	compactor := oc.getCompactor()
	settings := resolveMemoryFlushSettings(compactor.config)
	if settings == nil {
		return
	}
	contextWindow := oc.getModelContextWindow(meta)
	if contextWindow <= 0 {
		contextWindow = 128000
	}
	reserveTokens := compactor.config.ReserveTokens
	if reserveTokens <= 0 {
		reserveTokens = 2000
	}
	model := oc.effectiveModel(meta)
	totalTokens := estimatePromptTokens(prompt, model)
	if !oc.shouldRunMemoryFlush(meta, totalTokens, contextWindow, reserveTokens, settings) {
		return
	}

	flushPrompt := buildMemoryFlushPrompt(prompt, settings)
	if len(flushPrompt) == 0 {
		return
	}
	if err := oc.runMemoryFlushToolLoop(ctx, portal, meta, model, flushPrompt); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("memory flush failed")
		return
	}

	if meta != nil {
		meta.MemoryFlushAt = time.Now().UnixMilli()
		meta.MemoryFlushCompactionCount = meta.CompactionCount
		oc.savePortalQuiet(ctx, portal, "memory flush")
	}
}

func buildMemoryFlushPrompt(
	base []openai.ChatCompletionMessageParamUnion,
	settings *memoryFlushSettings,
) []openai.ChatCompletionMessageParamUnion {
	if settings == nil {
		return nil
	}
	trimmed := smartTruncatePrompt(base, 0.5)
	if len(trimmed) == 0 {
		trimmed = base
	}
	out := append([]openai.ChatCompletionMessageParamUnion{}, trimmed...)
	if strings.TrimSpace(settings.systemPrompt) != "" {
		insertAt := 0
		for insertAt < len(out) && out[insertAt].OfSystem != nil {
			insertAt++
		}
		systemMsg := openai.SystemMessage(settings.systemPrompt)
		out = append(out[:insertAt], append([]openai.ChatCompletionMessageParamUnion{systemMsg}, out[insertAt:]...)...)
	}
	out = append(out, openai.UserMessage(settings.prompt))
	return out
}

func (oc *AIClient) runMemoryFlushToolLoop(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	model string,
	messages []openai.ChatCompletionMessageParamUnion,
) error {
	if oc == nil {
		return errors.New("memory flush unavailable")
	}
	tools := memoryFlushTools()
	if len(tools) == 0 {
		return nil
	}
	toolParams := ToOpenAIChatTools(tools, &oc.log)
	toolParams = dedupeChatToolParams(toolParams)

	toolCtx := WithBridgeToolContext(ctx, &BridgeToolContext{
		Client: oc,
		Portal: portal,
		Meta:   meta,
	})

	flushCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	log := zerolog.Ctx(ctx)
	const maxTurns = 6
	for i := 0; i < maxTurns; i++ {
		req := openai.ChatCompletionNewParams{
			Model:    model,
			Messages: messages,
			Tools:    toolParams,
		}
		resp, err := oc.api.Chat.Completions.New(flushCtx, req)
		if err != nil {
			return err
		}
		if len(resp.Choices) == 0 {
			return nil
		}
		msg := resp.Choices[0].Message
		if len(msg.ToolCalls) == 0 {
			return nil
		}
		assistantParam := msg.ToAssistantMessageParam()
		messages = append(messages, openai.ChatCompletionMessageParamUnion{OfAssistant: &assistantParam})
		for _, call := range msg.ToolCalls {
			name := strings.TrimSpace(call.Function.Name)
			args := call.Function.Arguments
			result := ""
			var execErr error
			if name == "" {
				execErr = errors.New("missing tool name")
			} else if meta != nil && !oc.isToolEnabled(meta, name) {
				execErr = fmt.Errorf("tool %s is disabled", name)
			} else {
				result, execErr = oc.executeBuiltinTool(toolCtx, portal, name, args)
			}
			if execErr != nil {
				log.Warn().Err(execErr).Str("tool", name).Msg("memory flush tool failed")
				result = "Error: " + execErr.Error()
			}
			messages = append(messages, openai.ToolMessage(result, call.ID))
		}
	}
	return nil
}

func memoryFlushTools() []ToolDefinition {
	allowed := map[string]bool{
		ToolNameRead:  true,
		ToolNameWrite: true,
		ToolNameEdit:  true,
	}
	var out []ToolDefinition
	for _, tool := range BuiltinTools() {
		if allowed[tool.Name] {
			out = append(out, tool)
		}
	}
	return out
}

func estimatePromptTokens(prompt []openai.ChatCompletionMessageParamUnion, model string) int {
	if len(prompt) == 0 {
		return 0
	}
	if count, err := EstimateTokens(prompt, model); err == nil && count > 0 {
		return count
	}
	total := 0
	for _, msg := range prompt {
		total += estimateMessageChars(msg) / charsPerTokenEstimate
	}
	if total <= 0 {
		return len(prompt) * 3
	}
	return total
}
