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
