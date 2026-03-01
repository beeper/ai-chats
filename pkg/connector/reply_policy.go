package connector

import (
	"strings"

	runtimeparse "github.com/beeper/ai-bridge/pkg/runtime"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
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

func normalizeThreadReplies(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "off":
		return "off"
	case "always":
		return "always"
	case "inbound":
		return "inbound"
	}
	return ""
}

func (oc *AIClient) resolveMatrixReplyToMode() string {
	if oc != nil && oc.connector != nil && oc.connector.Config.Channels != nil && oc.connector.Config.Channels.Matrix != nil {
		if mode := runtimeparse.NormalizeReplyToMode(oc.connector.Config.Channels.Matrix.ReplyToMode); mode != "" {
			return string(mode)
		}
	}
	return "off"
}

func (oc *AIClient) resolveMatrixThreadReplies() string {
	if oc != nil && oc.connector != nil && oc.connector.Config.Channels != nil && oc.connector.Config.Channels.Matrix != nil {
		if normalized := normalizeThreadReplies(oc.connector.Config.Channels.Matrix.ThreadReplies); normalized != "" {
			return normalized
		}
	}
	return "inbound"
}

func (oc *AIClient) resolveInitialReplyTarget(evt *event.Event) ReplyTarget {
	mode := oc.resolveMatrixThreadReplies()
	if evt == nil {
		return ReplyTarget{}
	}
	ctx := extractInboundReplyContext(evt)
	switch mode {
	case "off":
		if ctx.ReplyTo != "" {
			return ReplyTarget{ReplyTo: ctx.ReplyTo}
		}
		return ReplyTarget{}
	case "inbound":
		if ctx.ThreadRoot != "" {
			return ReplyTarget{ReplyTo: ctx.ThreadRoot, ThreadRoot: ctx.ThreadRoot}
		}
		if ctx.ReplyTo != "" {
			return ReplyTarget{ReplyTo: ctx.ReplyTo}
		}
	case "always":
		root := ctx.ThreadRoot
		if root == "" {
			root = evt.ID
		}
		if root != "" {
			return ReplyTarget{ReplyTo: root, ThreadRoot: root}
		}
	}
	return ReplyTarget{}
}

func (oc *AIClient) queueThreadKey(evt *event.Event) string {
	mode := oc.resolveMatrixThreadReplies()
	if mode == "off" || evt == nil {
		return ""
	}
	ctx := extractInboundReplyContext(evt)
	switch mode {
	case "inbound":
		if ctx.ThreadRoot != "" {
			return ctx.ThreadRoot.String()
		}
		return ""
	case "always":
		if ctx.ThreadRoot != "" {
			return ctx.ThreadRoot.String()
		}
		if evt.ID != "" {
			return evt.ID.String()
		}
	}
	return ""
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
