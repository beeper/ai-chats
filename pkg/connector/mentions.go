package connector

import (
	"regexp"
	"strings"

	"github.com/beeper/ai-bridge/pkg/agents"
)

const mentionBackspaceChar = "\u0008"

func normalizeMentionPattern(pattern string) string {
	if !strings.Contains(pattern, mentionBackspaceChar) {
		return pattern
	}
	return strings.ReplaceAll(pattern, mentionBackspaceChar, `\b`)
}

func normalizeMentionPatterns(patterns []string) []string {
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		out = append(out, normalizeMentionPattern(trimmed))
	}
	return out
}

func deriveMentionPatterns(identity *agents.Identity) []string {
	if identity == nil {
		return nil
	}
	name := strings.TrimSpace(identity.Name)
	if name == "" {
		return nil
	}
	parts := strings.Fields(name)
	escaped := make([]string, 0, len(parts))
	for _, part := range parts {
		escaped = append(escaped, regexp.QuoteMeta(part))
	}
	re := strings.Join(escaped, `\s+`)
	if re == "" {
		re = regexp.QuoteMeta(name)
	}
	return []string{`\b@?` + re + `\b`}
}

func resolveMentionPatterns(cfg *Config, agent *agents.AgentDefinition) []string {
	if cfg != nil && cfg.Messages != nil && cfg.Messages.GroupChat != nil {
		if cfg.Messages.GroupChat.MentionPatterns != nil {
			return cfg.Messages.GroupChat.MentionPatterns
		}
	}
	if agent != nil && agent.Identity != nil {
		return deriveMentionPatterns(agent.Identity)
	}
	return nil
}

func buildMentionRegexes(cfg *Config, agent *agents.AgentDefinition) []*regexp.Regexp {
	patterns := normalizeMentionPatterns(resolveMentionPatterns(cfg, agent))
	if len(patterns) == 0 {
		return nil
	}
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile("(?i)" + p)
		if err != nil {
			continue
		}
		out = append(out, re)
	}
	return out
}

func normalizeMentionText(text string) string {
	if text == "" {
		return ""
	}
	return strings.ToLower(strings.Map(func(r rune) rune {
		switch {
		case r >= 0x200b && r <= 0x200f:
			return -1
		case r >= 0x202a && r <= 0x202e:
			return -1
		case r >= 0x2060 && r <= 0x206f:
			return -1
		default:
			return r
		}
	}, text))
}

func matchesMentionPatterns(text string, mentionRegexes []*regexp.Regexp) bool {
	if len(mentionRegexes) == 0 {
		return false
	}
	cleaned := normalizeMentionText(text)
	if cleaned == "" {
		return false
	}
	for _, re := range mentionRegexes {
		if re.MatchString(cleaned) {
			return true
		}
	}
	return false
}
