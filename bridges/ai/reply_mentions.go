package ai

import (
	"context"
	"regexp"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/agents"
)

// mentionContext holds the resolved mention-detection results shared across
// message, media, and text-file handlers.
type mentionContext struct {
	MentionRegexes  []*regexp.Regexp
	ReplyCtx        inboundReplyContext
	ExplicitMention bool
	HasExplicit     bool // true when msg.Content.Mentions was non-nil
	WasMentioned    bool
}

// resolveMentionContext performs agent-def resolution, mention-regex building,
// reply-context extraction, and explicit/pattern mention detection.
// textForPatterns is the text to match mention regexes against (rawBody or rawCaption).
func (oc *AIClient) resolveMentionContext(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	evt *event.Event,
	mentions *event.Mentions,
	textForPatterns string,
) mentionContext {
	var agentDef *agents.AgentDefinition
	if agentID := resolveAgentID(meta); agentID != "" {
		store := &AgentStoreAdapter{client: oc}
		if agent, err := store.GetAgentByID(ctx, agentID); err == nil {
			agentDef = agent
		}
	}
	regexes := buildMentionRegexes(&oc.connector.Config, agentDef)

	replyCtx := extractInboundReplyContext(evt)
	botMXID := oc.resolveBotMXID(ctx, portal, meta)

	explicit := false
	hasExplicit := false
	if mentions != nil {
		hasExplicit = true
		if mentions.Room || (botMXID != "" && mentions.Has(botMXID)) {
			explicit = true
		}
	}
	if !explicit && replyCtx.ReplyTo != "" {
		if oc.isReplyToBot(ctx, portal, replyCtx.ReplyTo) {
			explicit = true
		}
	}

	return mentionContext{
		MentionRegexes:  regexes,
		ReplyCtx:        replyCtx,
		ExplicitMention: explicit,
		HasExplicit:     hasExplicit,
		WasMentioned:    explicit || matchesMentionPatterns(textForPatterns, regexes),
	}
}

func (oc *AIClient) isReplyToBot(ctx context.Context, portal *bridgev2.Portal, replyTo id.EventID) bool {
	if oc == nil || portal == nil || replyTo == "" || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return false
	}
	msg, err := oc.loadPortalMessagePartByMXID(ctx, portal, replyTo)
	if err != nil || msg == nil {
		return false
	}
	sender := strings.TrimSpace(string(msg.SenderID))
	if sender == "" {
		return false
	}
	return strings.HasPrefix(sender, "model-") || strings.HasPrefix(sender, "agent-")
}
