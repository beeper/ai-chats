package connector

import (
	"strings"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	runtimeparse "github.com/beeper/ai-bridge/pkg/runtime"
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

func (oc *AIClient) resolveMatrixReplyToMode() string {
	if oc != nil && oc.connector != nil && oc.connector.Config.Channels != nil && oc.connector.Config.Channels.Matrix != nil {
		if mode := runtimeparse.NormalizeReplyToMode(oc.connector.Config.Channels.Matrix.ReplyToMode); mode != "" {
			return string(mode)
		}
	}
	return "off"
}

func (oc *AIClient) resolveMatrixThreadReplies() runtimeparse.ThreadReplyMode {
	if oc != nil && oc.connector != nil && oc.connector.Config.Channels != nil && oc.connector.Config.Channels.Matrix != nil {
		return runtimeparse.NormalizeThreadReplyMode(oc.connector.Config.Channels.Matrix.ThreadReplies)
	}
	return runtimeparse.ThreadReplyModeInbound
}

func (oc *AIClient) resolveInitialReplyTarget(evt *event.Event) ReplyTarget {
	mode := oc.resolveMatrixThreadReplies()
	if evt == nil {
		return ReplyTarget{}
	}
	ctx := extractInboundReplyContext(evt)
	decision := runtimeparse.ResolveInboundReplyTarget(mode, ctx.ReplyTo.String(), ctx.ThreadRoot.String(), evt.ID.String())
	target := ReplyTarget{}
	if strings.TrimSpace(decision.ReplyToID) != "" {
		target.ReplyTo = id.EventID(strings.TrimSpace(decision.ReplyToID))
	}
	if strings.TrimSpace(decision.ThreadRoot) != "" {
		target.ThreadRoot = id.EventID(strings.TrimSpace(decision.ThreadRoot))
	}
	return target
}

func (oc *AIClient) queueThreadKey(evt *event.Event) string {
	mode := oc.resolveMatrixThreadReplies()
	if mode == runtimeparse.ThreadReplyModeOff || evt == nil {
		return ""
	}
	ctx := extractInboundReplyContext(evt)
	return runtimeparse.ResolveQueueThreadKey(mode, ctx.ThreadRoot.String(), evt.ID.String())
}

func (oc *AIClient) resolveFinalReplyTarget(meta *PortalMetadata, state *streamingState, directives *runtimeparse.ReplyDirectiveResult) ReplyTarget {
	target := ReplyTarget{}
	if state != nil {
		target = state.replyTarget
	}

	replyMode := runtimeparse.NormalizeReplyToMode(oc.resolveMatrixReplyToMode())
	payload := runtimeparse.ReplyPayload{
		ReplyToID: string(target.ReplyTo),
	}
	if directives != nil {
		if directives.ReplyToID != "" {
			payload.ReplyToID = directives.ReplyToID
		}
		payload.ReplyToTag = directives.HasReplyTag
		payload.ReplyToCurrent = directives.ReplyToCurrent
	}
	applied := runtimeparse.ApplyReplyToMode([]runtimeparse.ReplyPayload{payload}, runtimeparse.ReplyThreadPolicy{
		Mode:                     replyMode,
		AllowExplicitWhenModeOff: false,
	})
	if len(applied) > 0 {
		target.ReplyTo = id.EventID(strings.TrimSpace(applied[0].ReplyToID))
	} else {
		target.ReplyTo = ""
	}
	if replyMode == runtimeparse.ReplyToModeOff {
		target.ThreadRoot = ""
	}
	if target.ReplyTo == "" && target.ThreadRoot != "" {
		target.ReplyTo = target.ThreadRoot
	}
	return target
}
