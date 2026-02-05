package connector

import (
	"context"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
)

const sessionGreetingPrompt = "A new session was started via /new or /reset. Greet the user in your configured persona, if one is provided. Be yourself - use your defined voice, mannerisms, and mood. Keep it to 1-3 sentences and ask what they want to do. If the runtime model differs from default_model in the system prompt, mention the default model. Do not mention internal steps, files, tools, or reasoning."
const autoGreetingPrompt = "A new chat was created. Greet the user in your configured persona, if one is provided. Be yourself - use your defined voice, mannerisms, and mood. Keep it to 1-3 sentences and ask what they want to do. If the runtime model differs from default_model in the system prompt, mention the default model. Do not mention internal steps, files, tools, or reasoning."

func maybePrependSessionGreeting(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt []openai.ChatCompletionMessageParamUnion,
	log zerolog.Logger,
) []openai.ChatCompletionMessageParamUnion {
	if meta == nil {
		return prompt
	}
	agentID := strings.TrimSpace(resolveAgentID(meta))
	if agentID == "" {
		return prompt
	}
	if meta.SessionBootstrapByAgent == nil {
		meta.SessionBootstrapByAgent = make(map[string]int64)
	}
	if meta.SessionBootstrapByAgent[agentID] != 0 {
		return prompt
	}
	meta.SessionBootstrapByAgent[agentID] = time.Now().UnixMilli()
	if portal != nil {
		if err := portal.Save(ctx); err != nil {
			log.Warn().Err(err).Msg("Failed to persist session bootstrap state")
		}
	}
	greeting := openai.SystemMessage(sessionGreetingPrompt)
	return append([]openai.ChatCompletionMessageParamUnion{greeting}, prompt...)
}
