package runtime

import (
	"regexp"
	"strings"
)

const SilentReplyToken = "NO_REPLY"

type InlineDirectiveParseOptions struct {
	CurrentMessageID    string
	StripAudioTag       bool
	StripReplyTags      bool
	NormalizeWhitespace bool
	SilentToken         string
}

type InlineDirectiveParseResult struct {
	Text              string
	AudioAsVoice      bool
	ReplyToID         string
	ReplyToExplicitID string
	ReplyToCurrent    bool
	HasAudioTag       bool
	HasReplyTag       bool
	IsSilent          bool
}

// toStreamingResult converts the parse result into a StreamingDirectiveResult,
// applying silent-reply detection and clearing the text when silent.
func (p *InlineDirectiveParseResult) toStreamingResult() *StreamingDirectiveResult {
	text := p.Text
	isSilent := isSilentForStreaming(text)
	if isSilent {
		text = ""
	}
	return &StreamingDirectiveResult{
		Text:              text,
		ReplyToExplicitID: p.ReplyToExplicitID,
		ReplyToCurrent:    p.ReplyToCurrent,
		HasReplyTag:       p.HasReplyTag,
		AudioAsVoice:      p.AudioAsVoice,
		IsSilent:          isSilent,
	}
}

// toReplyResult converts the parse result into a ReplyDirectiveResult,
// applying silent-reply detection and clearing the text when silent.
func (p *InlineDirectiveParseResult) toReplyResult() ReplyDirectiveResult {
	text := p.Text
	isSilent := IsSilentReplyText(text, SilentReplyToken)
	if isSilent {
		text = ""
	}
	return ReplyDirectiveResult{
		Text:              text,
		ReplyToID:         p.ReplyToID,
		ReplyToExplicitID: p.ReplyToExplicitID,
		ReplyToCurrent:    p.ReplyToCurrent,
		HasReplyTag:       p.HasReplyTag,
		AudioAsVoice:      p.AudioAsVoice,
		IsSilent:          isSilent,
	}
}

var (
	audioTagRE          = regexp.MustCompile(`(?i)\[\[\s*audio_as_voice\s*\]\]`)
	replyTagRE          = regexp.MustCompile(`(?i)\[\[\s*(?:reply_to_current|reply_to\s*:\s*([^\]\n]+))\s*\]\]`)
	collapseSpacesRE    = regexp.MustCompile(`[ \t]+`)
	normalizeNewlinesRE = regexp.MustCompile(`[ \t]*\n[ \t]*`)
)

func ParseInlineDirectives(text string, options InlineDirectiveParseOptions) InlineDirectiveParseResult {
	if text == "" {
		return InlineDirectiveParseResult{}
	}

	// When no explicit options are set, default to stripping both audio and reply tags.
	defaultStrip := !options.StripAudioTag && !options.StripReplyTags && !options.NormalizeWhitespace &&
		options.SilentToken == "" && options.CurrentMessageID == ""
	stripAudio := defaultStrip || options.StripAudioTag
	stripReply := defaultStrip || options.StripReplyTags

	cleaned := text
	result := InlineDirectiveParseResult{}

	cleaned = audioTagRE.ReplaceAllStringFunc(cleaned, func(match string) string {
		result.AudioAsVoice = true
		result.HasAudioTag = true
		if stripAudio {
			return " "
		}
		return match
	})

	var sawCurrent bool
	var explicit string
	cleaned = replyTagRE.ReplaceAllStringFunc(cleaned, func(match string) string {
		result.HasReplyTag = true
		sub := replyTagRE.FindStringSubmatch(match)
		if len(sub) > 1 && strings.TrimSpace(sub[1]) != "" {
			explicit = strings.TrimSpace(sub[1])
		} else {
			sawCurrent = true
		}
		if stripReply {
			return " "
		}
		return match
	})

	cleaned = normalizeDirectiveWhitespace(cleaned)

	if explicit != "" {
		result.ReplyToExplicitID = explicit
		result.ReplyToID = explicit
	} else if sawCurrent {
		result.ReplyToCurrent = true
		if strings.TrimSpace(options.CurrentMessageID) != "" {
			result.ReplyToID = strings.TrimSpace(options.CurrentMessageID)
		}
	}

	result.Text = cleaned
	return result
}

func isSilentForStreaming(text string) bool {
	return IsSilentReplyText(text, SilentReplyToken) || IsSilentReplyPrefixText(text, SilentReplyToken)
}

// IsSilentReplyText checks whether text is exactly the silent reply token (modulo whitespace).
func IsSilentReplyText(text, token string) bool {
	if text == "" {
		return false
	}
	if token = strings.TrimSpace(token); token == "" {
		token = SilentReplyToken
	}
	return strings.TrimSpace(text) == token
}

// IsSilentReplyPrefixText checks whether text is a partial-typing prefix of the silent token
// (e.g. "NO_RE" for "NO_REPLY"), used during streaming to detect silent replies early.
func IsSilentReplyPrefixText(text, token string) bool {
	if text == "" {
		return false
	}
	if token = strings.TrimSpace(token); token == "" {
		token = SilentReplyToken
	}
	normalized := strings.ToUpper(strings.TrimLeft(text, " \t\r\n"))
	if normalized == "" || !strings.Contains(normalized, "_") {
		return false
	}
	if !isUpperUnderscoreOnly(normalized) {
		return false
	}
	return strings.HasPrefix(strings.ToUpper(token), normalized)
}

func isUpperUnderscoreOnly(s string) bool {
	for _, c := range s {
		if !((c >= 'A' && c <= 'Z') || c == '_') {
			return false
		}
	}
	return true
}

func normalizeDirectiveWhitespace(text string) string {
	text = collapseSpacesRE.ReplaceAllString(text, " ")
	text = normalizeNewlinesRE.ReplaceAllString(text, "\n")
	return strings.TrimSpace(text)
}
