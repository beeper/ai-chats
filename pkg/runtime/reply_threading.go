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
	hasThreaded := false
	for _, payload := range payloads {
		if strings.TrimSpace(payload.ReplyToID) == "" {
			out = append(out, payload)
			continue
		}
		switch policy.Mode {
		case ReplyToModeAll:
			out = append(out, payload)
		case ReplyToModeFirst:
			if hasThreaded {
				payload.ReplyToID = ""
				payload.ReplyToCurrent = false
				payload.ReplyToTag = false
			}
			hasThreaded = true
			out = append(out, payload)
		case ReplyToModeOff:
			isExplicit := payload.ReplyToTag || payload.ReplyToCurrent
			if policy.AllowExplicitWhenModeOff && isExplicit {
				out = append(out, payload)
				continue
			}
			payload.ReplyToID = ""
			payload.ReplyToCurrent = false
			payload.ReplyToTag = false
			out = append(out, payload)
		}
	}
	return out
}
