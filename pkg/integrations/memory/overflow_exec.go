package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"

	iruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
)

const DefaultFlushSoftTokens = 4000

type FlushSettings struct {
	SoftThresholdTokens int
	Prompt              string
	SystemPrompt        string
}

type OverflowDeps struct {
	IsSimpleMode     func(call any) bool
	ResolveSettings  func() *FlushSettings
	TrimPrompt       func(prompt []openai.ChatCompletionMessageParamUnion) []openai.ChatCompletionMessageParamUnion
	ContextWindow    func(call any) int
	ReserveTokens    func() int
	EffectiveModel   func(call any) string
	EstimateTokens   func(prompt []openai.ChatCompletionMessageParamUnion, model string) int
	AlreadyFlushed   func(call any) bool
	MarkFlushed      func(ctx context.Context, call any)
	RunFlushToolLoop func(ctx context.Context, call any, model string, prompt []openai.ChatCompletionMessageParamUnion) (bool, error)
	OnError          func(ctx context.Context, err error)
}

func HandleOverflow(
	ctx context.Context,
	call any,
	prompt []openai.ChatCompletionMessageParamUnion,
	deps OverflowDeps,
) {
	if deps.IsSimpleMode != nil && deps.IsSimpleMode(call) {
		return
	}
	settings := deps.ResolveSettings
	if settings == nil {
		return
	}
	flushSettings := settings()
	if flushSettings == nil {
		return
	}
	contextWindow := 0
	if deps.ContextWindow != nil {
		contextWindow = deps.ContextWindow(call)
	}
	if contextWindow <= 0 {
		contextWindow = 128000
	}
	reserveTokens := 2000
	if deps.ReserveTokens != nil {
		if val := deps.ReserveTokens(); val > 0 {
			reserveTokens = val
		}
	}
	model := ""
	if deps.EffectiveModel != nil {
		model = deps.EffectiveModel(call)
	}
	totalTokens := 0
	if overflowCall, ok := call.(iruntime.ContextOverflowCall); ok && overflowCall.RequestedTokens > 0 {
		totalTokens = overflowCall.RequestedTokens
	}
	if totalTokens <= 0 && deps.EstimateTokens != nil {
		totalTokens = deps.EstimateTokens(prompt, model)
	}
	if !shouldRunFlush(totalTokens, contextWindow, reserveTokens, flushSettings, deps.AlreadyFlushed, call) {
		return
	}

	basePrompt := prompt
	if deps.TrimPrompt != nil {
		if trimmed := deps.TrimPrompt(prompt); len(trimmed) > 0 {
			basePrompt = trimmed
		}
	}
	flushPrompt := buildFlushPrompt(basePrompt, flushSettings)
	if len(flushPrompt) == 0 {
		return
	}
	if deps.RunFlushToolLoop == nil {
		return
	}
	ranFlush, err := deps.RunFlushToolLoop(ctx, call, model, flushPrompt)
	if err != nil {
		if deps.OnError != nil {
			deps.OnError(ctx, err)
		}
		return
	}
	if ranFlush && deps.MarkFlushed != nil {
		deps.MarkFlushed(ctx, call)
	}
}

func shouldRunFlush(
	totalTokens, contextWindow, reserveTokens int,
	settings *FlushSettings,
	alreadyFlushed func(call any) bool,
	call any,
) bool {
	if settings == nil {
		return false
	}
	if totalTokens <= 0 || contextWindow <= 0 {
		return false
	}
	threshold := contextWindow - reserveTokens - settings.SoftThresholdTokens
	if threshold <= 0 || totalTokens < threshold {
		return false
	}
	if alreadyFlushed != nil && alreadyFlushed(call) {
		return false
	}
	return true
}

func buildFlushPrompt(base []openai.ChatCompletionMessageParamUnion, settings *FlushSettings) []openai.ChatCompletionMessageParamUnion {
	if settings == nil {
		return nil
	}
	trimmed := append([]openai.ChatCompletionMessageParamUnion{}, base...)
	if strings.TrimSpace(settings.SystemPrompt) != "" {
		insertAt := 0
		for insertAt < len(trimmed) && trimmed[insertAt].OfSystem != nil {
			insertAt++
		}
		systemMsg := openai.SystemMessage(settings.SystemPrompt)
		trimmed = append(trimmed[:insertAt], append([]openai.ChatCompletionMessageParamUnion{systemMsg}, trimmed[insertAt:]...)...)
	}
	if strings.TrimSpace(settings.Prompt) != "" {
		trimmed = append(trimmed, openai.UserMessage(settings.Prompt))
	}
	return trimmed
}

func EnsureSilentReplyHint(token string, text string) string {
	if strings.Contains(text, token) {
		return text
	}
	return text + "\n\nIf no user-visible reply is needed, start with " + token + "."
}

func NormalizeFlushSettings(
	enabled *bool,
	softThresholdTokens int,
	prompt string,
	systemPrompt string,
	defaultPrompt string,
	defaultSystemPrompt string,
	silentToken string,
) *FlushSettings {
	// Keep compaction independent from memory unless explicitly enabled.
	if enabled == nil || !*enabled {
		return nil
	}
	soft := DefaultFlushSoftTokens
	if softThresholdTokens > 0 {
		soft = softThresholdTokens
	}
	p := strings.TrimSpace(prompt)
	if p == "" {
		p = defaultPrompt
	}
	sp := strings.TrimSpace(systemPrompt)
	if sp == "" {
		sp = defaultSystemPrompt
	}
	return &FlushSettings{
		SoftThresholdTokens: soft,
		Prompt:              EnsureSilentReplyHint(silentToken, p),
		SystemPrompt:        EnsureSilentReplyHint(silentToken, sp),
	}
}

func DefaultFlushPrompts(silentToken string) (prompt string, systemPrompt string) {
	prompt = strings.TrimSpace(strings.Join([]string{
		"Pre-compaction memory flush.",
		"Store durable memories now (use memory/YYYY-MM-DD.md; create memory/ if needed).",
		fmt.Sprintf("If nothing to store, reply with %s.", silentToken),
	}, " "))
	systemPrompt = strings.TrimSpace(strings.Join([]string{
		"Pre-compaction memory flush turn.",
		"The session is near auto-compaction; capture durable memories to disk.",
		fmt.Sprintf("You may reply, but usually %s is correct.", silentToken),
	}, " "))
	return prompt, systemPrompt
}
