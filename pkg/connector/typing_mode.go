package connector

import (
	"strings"
	"time"
)

type TypingMode string

const (
	TypingModeNever    TypingMode = "never"
	TypingModeInstant  TypingMode = "instant"
	TypingModeThinking TypingMode = "thinking"
	TypingModeMessage  TypingMode = "message"
)

const defaultTypingInterval = 6 * time.Second

func normalizeTypingMode(raw string) (TypingMode, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "never":
		return TypingModeNever, true
	case "instant":
		return TypingModeInstant, true
	case "thinking":
		return TypingModeThinking, true
	case "message":
		return TypingModeMessage, true
	default:
	}
	return "", false
}

func (oc *AIClient) resolveTypingMode(meta *PortalMetadata, ctx *TypingContext, isHeartbeat bool) TypingMode {
	if isHeartbeat {
		return TypingModeNever
	}
	if meta != nil {
		if mode, ok := normalizeTypingMode(meta.TypingMode); ok {
			return mode
		}
	}
	agentID := normalizeAgentID(resolveAgentID(meta))
	if oc != nil && oc.connector != nil && oc.connector.Config.Agents != nil {
		for _, entry := range oc.connector.Config.Agents.List {
			if normalizeAgentID(entry.ID) != agentID {
				continue
			}
			if mode, ok := normalizeTypingMode(entry.TypingMode); ok {
				return mode
			}
		}
		if defaults := oc.connector.Config.Agents.Defaults; defaults != nil {
			if mode, ok := normalizeTypingMode(defaults.TypingMode); ok {
				return mode
			}
		}
	}
	isGroup := false
	wasMentioned := false
	if ctx != nil {
		isGroup = ctx.IsGroup
		wasMentioned = ctx.WasMentioned
	}
	if !isGroup || wasMentioned {
		return TypingModeInstant
	}
	return TypingModeMessage
}

func (oc *AIClient) resolveTypingInterval(meta *PortalMetadata) time.Duration {
	interval := defaultTypingInterval
	if meta != nil && meta.TypingIntervalSeconds != nil {
		interval = time.Duration(*meta.TypingIntervalSeconds) * time.Second
		if interval <= 0 {
			return 0
		}
		return interval
	}
	agentID := normalizeAgentID(resolveAgentID(meta))
	if oc != nil && oc.connector != nil && oc.connector.Config.Agents != nil {
		for _, entry := range oc.connector.Config.Agents.List {
			if normalizeAgentID(entry.ID) != agentID {
				continue
			}
			if entry.TypingIntervalSec != nil {
				interval = time.Duration(*entry.TypingIntervalSec) * time.Second
				if interval <= 0 {
					return 0
				}
				return interval
			}
			break
		}
		if defaults := oc.connector.Config.Agents.Defaults; defaults != nil && defaults.TypingIntervalSec != nil {
			interval = time.Duration(*defaults.TypingIntervalSec) * time.Second
		}
	}
	if interval <= 0 {
		return 0
	}
	return interval
}

type TypingSignaler struct {
	mode                 TypingMode
	typing               *TypingController
	disabled             bool
	shouldStartImmediate bool
	shouldStartOnText    bool
	shouldStartOnReason  bool
	hasRenderableText    bool
}

func NewTypingSignaler(typing *TypingController, mode TypingMode, isHeartbeat bool) *TypingSignaler {
	disabled := isHeartbeat || mode == TypingModeNever || typing == nil
	return &TypingSignaler{
		mode:                 mode,
		typing:               typing,
		disabled:             disabled,
		shouldStartImmediate: mode == TypingModeInstant,
		shouldStartOnText:    mode == TypingModeMessage || mode == TypingModeInstant,
		shouldStartOnReason:  mode == TypingModeThinking,
	}
}

func (ts *TypingSignaler) SignalRunStart() {
	if ts == nil || ts.disabled || !ts.shouldStartImmediate {
		return
	}
	ts.typing.Start()
}

func (ts *TypingSignaler) SignalTextDelta(text string) {
	if ts == nil || ts.disabled {
		return
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	renderable := !isSilentReplyText(trimmed)
	if renderable {
		ts.hasRenderableText = true
	} else {
		return
	}

	if ts.shouldStartOnText {
		ts.typing.Start()
		ts.typing.RefreshTTL()
		return
	}
	if ts.shouldStartOnReason {
		if !ts.typing.IsActive() {
			ts.typing.Start()
		}
		ts.typing.RefreshTTL()
	}
}

func (ts *TypingSignaler) SignalReasoningDelta() {
	if ts == nil || ts.disabled || !ts.shouldStartOnReason {
		return
	}
	if !ts.hasRenderableText {
		return
	}
	ts.typing.Start()
	ts.typing.RefreshTTL()
}

func (ts *TypingSignaler) SignalToolStart() {
	if ts == nil || ts.disabled {
		return
	}
	if !ts.typing.IsActive() {
		ts.typing.Start()
	}
	ts.typing.RefreshTTL()
}
