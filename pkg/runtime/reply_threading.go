package runtime

import "strings"

func NormalizeReplyToMode(raw string) ReplyToMode {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case string(ReplyToModeOff):
		return ReplyToModeOff
	case string(ReplyToModeFirst):
		return ReplyToModeFirst
	case string(ReplyToModeAll):
		return ReplyToModeAll
	default:
		return ReplyToModeOff
	}
}

type ReplyThreadPolicy struct {
	Mode                     ReplyToMode
	AllowExplicitWhenModeOff bool
}

func ApplyReplyToMode(payloads []ReplyPayload, policy ReplyThreadPolicy) []ReplyPayload {
	out := make([]ReplyPayload, 0, len(payloads))
	seenFirst := false
	for _, payload := range payloads {
		if strings.TrimSpace(payload.ReplyToID) != "" {
			clear := false
			switch policy.Mode {
			case ReplyToModeFirst:
				clear = seenFirst
				seenFirst = true
			case ReplyToModeOff:
				isExplicit := payload.ReplyToTag || payload.ReplyToCurrent
				clear = !policy.AllowExplicitWhenModeOff || !isExplicit
			}
			if clear {
				payload.ReplyToID = ""
				payload.ReplyToCurrent = false
				payload.ReplyToTag = false
			}
		}
		out = append(out, payload)
	}
	return out
}

type ThreadReplyMode string

const (
	ThreadReplyModeOff     ThreadReplyMode = "off"
	ThreadReplyModeInbound ThreadReplyMode = "inbound"
	ThreadReplyModeAlways  ThreadReplyMode = "always"
)

func NormalizeThreadReplyMode(raw string) ThreadReplyMode {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case string(ThreadReplyModeOff):
		return ThreadReplyModeOff
	case string(ThreadReplyModeAlways):
		return ThreadReplyModeAlways
	default:
		return ThreadReplyModeInbound
	}
}

func ResolveInboundReplyTarget(mode ThreadReplyMode, replyToID, threadRootID, eventID string) ReplyTargetDecision {
	replyToID = strings.TrimSpace(replyToID)
	threadRootID = strings.TrimSpace(threadRootID)
	eventID = strings.TrimSpace(eventID)

	switch mode {
	case ThreadReplyModeOff:
		return ReplyTargetDecision{
			ReplyToID:  replyToID,
			ThreadRoot: "",
			Reason:     "threading_off",
		}
	case ThreadReplyModeAlways:
		root := threadRootID
		if root == "" {
			root = eventID
		}
		return ReplyTargetDecision{
			ReplyToID:  root,
			ThreadRoot: root,
			Reason:     "threading_always",
		}
	default: // ThreadReplyModeInbound
		if threadRootID != "" {
			return ReplyTargetDecision{
				ReplyToID:  threadRootID,
				ThreadRoot: threadRootID,
				Reason:     "threading_inbound_thread",
			}
		}
		return ReplyTargetDecision{
			ReplyToID: replyToID,
			Reason:    "threading_inbound_reply",
		}
	}
}

func ResolveQueueThreadKey(mode ThreadReplyMode, threadRootID, eventID string) string {
	threadRootID = strings.TrimSpace(threadRootID)
	eventID = strings.TrimSpace(eventID)
	switch mode {
	case ThreadReplyModeAlways:
		if threadRootID != "" {
			return threadRootID
		}
		return eventID
	case ThreadReplyModeInbound:
		return threadRootID
	default:
		return ""
	}
}
