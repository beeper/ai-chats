package runtime

import "strings"

func IsRenderablePayload(payload ReplyPayload) bool {
	if strings.TrimSpace(payload.Text) != "" {
		return true
	}
	if strings.TrimSpace(payload.MediaURL) != "" {
		return true
	}
	return len(payload.MediaURLs) > 0
}

func NormalizeReplyPayloadDirectives(payload ReplyPayload, currentMessageID string) (ReplyPayload, bool) {
	parsed := ParseReplyDirectives(payload.Text, currentMessageID)
	payload.Text = parsed.Text
	if payload.ReplyToID == "" {
		payload.ReplyToID = parsed.ReplyToID
	}
	payload.ReplyToCurrent = payload.ReplyToCurrent || parsed.ReplyToCurrent
	payload.ReplyToTag = payload.ReplyToTag || parsed.HasReplyTag
	payload.AudioAsVoice = payload.AudioAsVoice || parsed.AudioAsVoice
	return payload, parsed.IsSilent
}
