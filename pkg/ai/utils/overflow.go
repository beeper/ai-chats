package utils

import (
	"regexp"

	"github.com/beeper/ai-bridge/pkg/ai"
)

var overflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)prompt is too long`),
	regexp.MustCompile(`(?i)input is too long for requested model`),
	regexp.MustCompile(`(?i)exceeds the context window`),
	regexp.MustCompile(`(?i)input token count.*exceeds the maximum`),
	regexp.MustCompile(`(?i)maximum prompt length is \d+`),
	regexp.MustCompile(`(?i)reduce the length of the messages`),
	regexp.MustCompile(`(?i)maximum context length is \d+ tokens`),
	regexp.MustCompile(`(?i)exceeds the limit of \d+`),
	regexp.MustCompile(`(?i)exceeds the available context size`),
	regexp.MustCompile(`(?i)greater than the context length`),
	regexp.MustCompile(`(?i)context window exceeds limit`),
	regexp.MustCompile(`(?i)exceeded model token limit`),
	regexp.MustCompile(`(?i)context[_ ]length[_ ]exceeded`),
	regexp.MustCompile(`(?i)too many tokens`),
	regexp.MustCompile(`(?i)token limit exceeded`),
}

func IsContextOverflow(message ai.Message, contextWindow int) bool {
	if message.StopReason == ai.StopReasonError && message.ErrorMessage != "" {
		for _, p := range overflowPatterns {
			if p.MatchString(message.ErrorMessage) {
				return true
			}
		}
		if regexp.MustCompile(`(?i)^4(00|13)\s*(status code)?\s*\(no body\)`).MatchString(message.ErrorMessage) {
			return true
		}
	}

	if contextWindow > 0 && message.StopReason == ai.StopReasonStop {
		inputTokens := message.Usage.Input + message.Usage.CacheRead
		return inputTokens > contextWindow
	}
	return false
}

func GetOverflowPatterns() []*regexp.Regexp {
	result := make([]*regexp.Regexp, 0, len(overflowPatterns))
	result = append(result, overflowPatterns...)
	return result
}
