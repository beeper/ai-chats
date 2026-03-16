package ai

import (
	"context"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
)

// buildSimpleModeSystemPrompt returns the system prompt for simple mode rooms.
// Simple mode uses a single system prompt with only the current time appended.
func (oc *AIClient) buildSimpleModeSystemPrompt(meta *PortalMetadata) string {
	base := defaultSimpleModeSystemPrompt
	timezone, _ := oc.resolveUserTimezone()
	now := formatCurrentTimeForPrompt(timezone)

	lines := []string{strings.TrimSpace(base)}
	if supplement := strings.TrimSpace(oc.profilePromptSupplement()); supplement != "" {
		lines = append(lines, supplement)
	}
	lines = append(lines, "Current time: "+now)
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// buildSystemMessages returns the system prompt messages for the current mode.
func (oc *AIClient) buildSystemMessages(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) []openai.ChatCompletionMessageParamUnion {
	if isSimpleMode(meta) {
		return []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(oc.buildSimpleModeSystemPrompt(meta)),
		}
	}
	var msgs []openai.ChatCompletionMessageParamUnion
	systemPrompt := oc.effectiveAgentPrompt(ctx, portal, meta)
	if systemPrompt == "" {
		systemPrompt = oc.effectivePrompt(meta)
	}
	if systemPrompt != "" {
		msgs = append(msgs, openai.SystemMessage(systemPrompt))
	}
	msgs = append(msgs, oc.buildAdditionalSystemPrompts(ctx, portal, meta)...)
	return msgs
}

func formatCurrentTimeForPrompt(timezone string) string {
	loc := time.UTC
	if tz := strings.TrimSpace(timezone); tz != "" {
		if loaded, err := time.LoadLocation(tz); err == nil {
			loc = loaded
		}
	}
	now := time.Now().In(loc)
	return now.Format("Monday, January 2, 2006 - 3:04 PM (MST)")
}

// cleanHistoryBody normalizes history body text for the current mode.
func cleanHistoryBody(body string, simple bool, _ id.EventID) string {
	if simple {
		body = airuntime.SanitizeChatMessageForDisplay(body, true)
	}
	return body
}
