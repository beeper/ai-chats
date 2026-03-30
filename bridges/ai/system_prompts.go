package ai

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	integrationruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
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

func (oc *AIClient) buildAdditionalSystemPromptText(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
) string {
	return joinPromptFragments(
		oc.buildAdditionalSystemPromptCoreText(ctx, portal, meta),
		oc.buildMemoryPromptContextText(ctx, portal, meta),
	)
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
	return joinPromptFragments(base, oc.buildAdditionalSystemPromptText(ctx, portal, meta))
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

func (oc *AIClient) buildAdditionalSystemPromptCoreText(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
) string {
	var out []string

	if meta != nil && portal != nil && oc.isGroupChat(ctx, portal) {
		activation := oc.resolveGroupActivation(meta)
		intro := buildGroupIntro(oc.matrixRoomDisplayName(ctx, portal), activation)
		if strings.TrimSpace(intro) != "" {
			out = append(out, intro)
		}
	}

	if accountHint := oc.buildDesktopAccountHintPrompt(ctx); accountHint != "" {
		out = append(out, accountHint)
	}

	if ident := buildSessionIdentityHint(portal, meta); ident != "" {
		out = append(out, ident)
	}

	return joinPromptFragments(out...)
}

func (oc *AIClient) buildMemoryPromptContextText(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
) string {
	if oc == nil || len(oc.integrationModules) == 0 {
		return ""
	}
	module := oc.integrationModules["memory"]
	augmentor, ok := module.(integrationruntime.PromptContextIntegration)
	if !ok || augmentor == nil {
		return ""
	}
	return strings.TrimSpace(augmentor.PromptContextText(ctx, integrationruntime.PromptScope{
		Portal: portal,
		Meta:   meta,
	}))
}
