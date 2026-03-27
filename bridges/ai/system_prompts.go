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

func (oc *AIClient) buildSystemMessages(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
) []openai.ChatCompletionMessageParamUnion {
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
