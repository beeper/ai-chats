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

	stripAudio := true
	stripReply := true
	// Keep a compatibility guard for zero-value options while preserving
	// OpenClaw defaults (strip audio/reply tags by default).
	if options.StripAudioTag || options.StripReplyTags || options.NormalizeWhitespace || options.SilentToken != "" || options.CurrentMessageID != "" {
		stripAudio = options.StripAudioTag
		stripReply = options.StripReplyTags
	}

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

	// OpenClaw normalizes whitespace after inline tag stripping.
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

var nonUpperUnderscoreRE = regexp.MustCompile(`[^A-Z_]`)

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
	if nonUpperUnderscoreRE.MatchString(normalized) {
		return false
	}
	return strings.HasPrefix(strings.ToUpper(token), normalized)
}

func normalizeDirectiveWhitespace(text string) string {
	text = collapseSpacesRE.ReplaceAllString(text, " ")
	text = normalizeNewlinesRE.ReplaceAllString(text, "\n")
	return strings.TrimSpace(text)
}
