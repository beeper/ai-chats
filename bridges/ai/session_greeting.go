package ai

import (
	"context"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
)

const sessionGreetingPrompt = "A new session was started via !ai reset. Greet the user in your configured persona, if one is provided. Be yourself - use your defined voice, mannerisms, and mood. Keep it to 1-3 sentences and ask what they want to do. If the runtime model differs from default_model in the system prompt, mention the default model. Do not mention internal steps, files, tools, or reasoning."
const autoGreetingPrompt = "A new chat was created. Greet the user in your configured persona, if one is provided. Be yourself - use your defined voice, mannerisms, and mood. Keep it to 1-3 sentences and ask what they want to do. If the runtime model differs from default_model in the system prompt, mention the default model. Do not mention internal steps, files, tools, or reasoning."

func maybePrependSessionGreeting(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt []openai.ChatCompletionMessageParamUnion,
	log zerolog.Logger,
) []openai.ChatCompletionMessageParamUnion {
	if greeting := sessionGreetingFragment(ctx, portal, meta, log); greeting != "" {
		return append([]openai.ChatCompletionMessageParamUnion{openai.SystemMessage(greeting)}, prompt...)
	}
	return prompt
}

func sessionGreetingFragment(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	log zerolog.Logger,
) string {
	if meta == nil {
		return ""
	}
	agentID := strings.TrimSpace(resolveAgentID(meta))
	if agentID == "" {
		return ""
	}
	if meta.SessionBootstrapByAgent == nil {
		meta.SessionBootstrapByAgent = make(map[string]int64)
	}
	if meta.SessionBootstrapByAgent[agentID] != 0 {
		return ""
	}
	meta.SessionBootstrapByAgent[agentID] = time.Now().UnixMilli()
	if portal != nil {
		if err := portal.Save(ctx); err != nil {
			log.Warn().Err(err).Msg("Failed to persist session bootstrap state")
		}
	}
	return sessionGreetingPrompt
}
