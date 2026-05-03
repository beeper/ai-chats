package ai

import (
	"strings"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	runtimeparse "github.com/beeper/agentremote/pkg/runtime"
)

type ReplyTarget struct {
	ReplyTo    id.EventID
	ThreadRoot id.EventID
}

func (t ReplyTarget) EffectiveReplyTo() id.EventID {
	if t.ReplyTo != "" {
		return t.ReplyTo
	}
	return t.ThreadRoot
}

type inboundReplyContext struct {
	ReplyTo    id.EventID
	ThreadRoot id.EventID
}

func extractInboundReplyContext(evt *event.Event) inboundReplyContext {
	if evt == nil || evt.Content.Raw == nil {
		return inboundReplyContext{}
	}
	raw, ok := evt.Content.Raw["m.relates_to"].(map[string]any)
	if !ok || raw == nil {
		return inboundReplyContext{}
	}
	ctx := inboundReplyContext{}
	if inReply, ok := raw["m.in_reply_to"].(map[string]any); ok {
		if value, ok := inReply["event_id"].(string); ok && strings.TrimSpace(value) != "" {
			ctx.ReplyTo = id.EventID(strings.TrimSpace(value))
		}
	}
	if relType, ok := raw["rel_type"].(string); ok && relType == RelThread {
		if value, ok := raw["event_id"].(string); ok && strings.TrimSpace(value) != "" {
			ctx.ThreadRoot = id.EventID(strings.TrimSpace(value))
		}
	}
	return ctx
}

func (oc *AIClient) resolveInitialReplyTarget(evt *event.Event) ReplyTarget {
	if evt == nil {
		return ReplyTarget{}
	}
	ctx := extractInboundReplyContext(evt)
	target := ReplyTarget(ctx)
	if target.ReplyTo == "" && target.ThreadRoot != "" {
		target.ReplyTo = target.ThreadRoot
	}
	return target
}

func (oc *AIClient) queueThreadKey(evt *event.Event) string {
	if evt == nil {
		return ""
	}
	ctx := extractInboundReplyContext(evt)
	return ctx.ThreadRoot.String()
}

func (oc *AIClient) resolveFinalReplyTarget(meta *PortalMetadata, state *streamingState, directives *runtimeparse.ReplyDirectiveResult) ReplyTarget {
	target := ReplyTarget{}
	if state != nil {
		target = state.replyTarget
	}

	if directives != nil && directives.HasReplyTag {
		if strings.TrimSpace(directives.ReplyToID) != "" {
			target.ReplyTo = id.EventID(strings.TrimSpace(directives.ReplyToID))
		} else if directives.ReplyToCurrent && state != nil && state.sourceEventID() != "" {
			target.ReplyTo = state.sourceEventID()
		}
	}
	if target.ReplyTo == "" && target.ThreadRoot != "" {
		target.ReplyTo = target.ThreadRoot
	}
	return target
}
