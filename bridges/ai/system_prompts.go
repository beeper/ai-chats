package ai

import (
	"context"
	"strings"

	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"

	runtimeparse "github.com/beeper/agentremote/pkg/runtime"
)

func buildGroupIntro(roomName string, activation string) string {
	subjectLine := "You are replying inside a bridged group chat (Matrix room)."
	if strings.TrimSpace(roomName) != "" {
		subjectLine = "You are replying inside the group \"" + strings.TrimSpace(roomName) + "\" (Matrix room)."
	}
	activationLine := "Activation: trigger-only (you are invoked only when explicitly mentioned; recent context may be included)."
	if activation == "always" {
		activationLine = "Activation: always-on (you receive every group message)."
	}
	lines := []string{subjectLine, activationLine}
	if activation == "always" {
		lines = append(lines,
			"If no response is needed, reply with exactly \""+runtimeparse.SilentReplyToken+"\" (and nothing else) so the bridge stays silent.",
			"Be extremely selective: reply only when directly addressed or clearly helpful. Otherwise stay silent.",
		)
	}
	lines = append(lines,
		"Be a good group participant: mostly lurk and follow the conversation; reply only when directly addressed or you can add clear value.",
		"Write like a human. Avoid Markdown tables. Use real line breaks sparingly.",
	)
	return strings.Join(lines, " ") + " Address the specific sender noted in the message context."
}

func buildVerboseSystemHint(_ *PortalMetadata) string {
	return ""
}

func buildSessionIdentityHint(portal *bridgev2.Portal, _ *PortalMetadata) string {
	if portal == nil {
		return ""
	}

	// Use a single identifier to avoid confusing the model.
	// This should match what tools call "sessionKey".
	session := ""
	if portal.MXID != "" {
		session = strings.TrimSpace(portal.MXID.String())
	}
	if session == "" {
		return ""
	}

	return "sessionKey: " + session
}

func (oc *AIClient) buildAdditionalSystemPrompts(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
) []openai.ChatCompletionMessageParamUnion {
	return oc.additionalSystemMessages(ctx, portal, meta)
}

func systemMessageText(messages []openai.ChatCompletionMessageParamUnion) string {
	var parts []string
	for _, msg := range messages {
		if msg.OfSystem == nil {
			continue
		}
		if text := strings.TrimSpace(msg.OfSystem.Content.OfString.Value); text != "" {
			parts = append(parts, text)
			continue
		}
		if len(msg.OfSystem.Content.OfArrayOfContentParts) == 0 {
			continue
		}
		var lines []string
		for _, part := range msg.OfSystem.Content.OfArrayOfContentParts {
			if text := strings.TrimSpace(part.Text); text != "" {
				lines = append(lines, text)
			}
		}
		if len(lines) > 0 {
			parts = append(parts, strings.Join(lines, "\n"))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func (oc *AIClient) buildSystemPromptText(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
) string {
	base := oc.effectiveAgentPrompt(ctx, portal, meta)
	if base == "" {
		base = oc.effectivePrompt(meta)
	}
	fragments := []string{base, systemMessageText(oc.buildAdditionalSystemPrompts(ctx, portal, meta))}
	var parts []string
	for _, fragment := range fragments {
		if text := strings.TrimSpace(fragment); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func (oc *AIClient) buildConversationSystemPromptText(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	includeGreeting bool,
) string {
	base := oc.buildSystemPromptText(ctx, portal, meta)
	if !includeGreeting {
		return base
	}
	return joinPromptFragments(sessionGreetingFragment(ctx, portal, meta, oc.log), base)
}

func (oc *AIClient) buildAdditionalSystemPromptsCore(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
) []openai.ChatCompletionMessageParamUnion {
	var out []openai.ChatCompletionMessageParamUnion

	if meta != nil && portal != nil && oc.isGroupChat(ctx, portal) {
		activation := oc.resolveGroupActivation(meta)
		intro := buildGroupIntro(oc.matrixRoomDisplayName(ctx, portal), activation)
		if strings.TrimSpace(intro) != "" {
			out = append(out, openai.SystemMessage(intro))
		}
	}

	if meta != nil {
		if verboseHint := buildVerboseSystemHint(meta); verboseHint != "" {
			out = append(out, openai.SystemMessage(verboseHint))
		}
	}

	if accountHint := oc.buildDesktopAccountHintPrompt(ctx); accountHint != "" {
		out = append(out, openai.SystemMessage(accountHint))
	}

	if ident := buildSessionIdentityHint(portal, meta); ident != "" {
		out = append(out, openai.SystemMessage(ident))
	}

	return out
}
