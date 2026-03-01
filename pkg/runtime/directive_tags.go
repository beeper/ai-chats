package runtime

import (
	"regexp"
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
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
	audioTagRE = regexp.MustCompile(`(?i)\[\[\s*audio_as_voice\s*\]\]`)
	replyTagRE = regexp.MustCompile(`(?i)\[\[\s*(?:reply_to_current|reply_to\s*:\s*([^\]\n]+))\s*\]\]`)

	silentPrefixRE = regexp.MustCompile(`^\s*` + regexp.QuoteMeta(SilentReplyToken) + `(?:$|\W)`)
	silentSuffixRE = regexp.MustCompile(`\b` + regexp.QuoteMeta(SilentReplyToken) + `\b\W*$`)

	collapseSpacesRE    = regexp.MustCompile(`[ \t]+`)
	normalizeNewlinesRE = regexp.MustCompile(`[ \t]*\n[ \t]*`)
	collapseNewlinesRE  = regexp.MustCompile(`\n{3,}`)
)

func ParseInlineDirectives(text string, options InlineDirectiveParseOptions) InlineDirectiveParseResult {
	if text == "" {
		return InlineDirectiveParseResult{}
	}

	stripAudio := true
	if options.StripAudioTag || options.StripReplyTags || options.NormalizeWhitespace || options.SilentToken != "" || options.CurrentMessageID != "" {
		stripAudio = options.StripAudioTag
	}
	stripReply := true
	if options.StripReplyTags || options.CurrentMessageID != "" || options.SilentToken != "" || options.NormalizeWhitespace {
		stripReply = options.StripReplyTags
	}
	normalizeWhitespace := true
	if options.NormalizeWhitespace || options.CurrentMessageID != "" || options.SilentToken != "" {
		normalizeWhitespace = options.NormalizeWhitespace
	}
	silentToken := options.SilentToken
	if strings.TrimSpace(silentToken) == "" {
		silentToken = SilentReplyToken
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

	if normalizeWhitespace {
		cleaned = NormalizeDirectiveWhitespace(cleaned)
	}

	if explicit != "" {
		result.ReplyToExplicitID = explicit
		result.ReplyToID = explicit
	} else if sawCurrent {
		result.ReplyToCurrent = true
		if strings.TrimSpace(options.CurrentMessageID) != "" {
			result.ReplyToID = strings.TrimSpace(options.CurrentMessageID)
		}
	}

	if IsSilentReplyText(cleaned, silentToken) {
		result.IsSilent = true
		cleaned = ""
	}

	result.Text = cleaned
	return result
}

func IsSilentReplyText(text, token string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	token = strings.TrimSpace(token)
	if token == "" {
		token = SilentReplyToken
	}
	stripped := stringutil.StripMarkup(text)
	trimmed := strings.TrimSpace(stripped)
	if trimmed == token {
		return true
	}
	prefix := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(token) + `(?:$|\W)`)
	suffix := regexp.MustCompile(`\b` + regexp.QuoteMeta(token) + `\b\W*$`)
	return prefix.MatchString(stripped) || suffix.MatchString(stripped)
}

func NormalizeDirectiveWhitespace(text string) string {
	text = collapseSpacesRE.ReplaceAllString(text, " ")
	text = normalizeNewlinesRE.ReplaceAllString(text, "\n")
	text = collapseNewlinesRE.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}
