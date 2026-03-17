package sdk

import (
	"strings"
	"time"
)

type StreamPartState struct {
	visible         strings.Builder
	accumulated     strings.Builder
	lastVisibleText string
	finishReason    string
	errorText       string
	startedAtMs     int64
	firstTokenAtMs  int64
	completedAtMs   int64
}

func (s *StreamPartState) ApplyPart(part map[string]any, partTimestamp time.Time) {
	if s == nil || len(part) == 0 {
		return
	}
	partType := strings.TrimSpace(stringValue(part["type"]))
	if partType == "" {
		return
	}
	s.applyPartTimestamp(partType, partTimestamp)
	nowMillis := time.Now().UnixMilli()
	switch partType {
	case "start":
		if s.startedAtMs == 0 {
			s.startedAtMs = timestampMillis(partTimestamp, nowMillis)
		}
	case "text-delta":
		if delta := strings.TrimSpace(stringValue(part["delta"])); delta != "" {
			s.visible.WriteString(delta)
			s.accumulated.WriteString(delta)
			if s.firstTokenAtMs == 0 {
				s.firstTokenAtMs = timestampMillis(partTimestamp, nowMillis)
			}
			if s.startedAtMs == 0 {
				s.startedAtMs = timestampMillis(partTimestamp, nowMillis)
			}
		}
	case "reasoning-delta":
		if delta := strings.TrimSpace(stringValue(part["delta"])); delta != "" {
			s.accumulated.WriteString(delta)
			if s.firstTokenAtMs == 0 {
				s.firstTokenAtMs = timestampMillis(partTimestamp, nowMillis)
			}
			if s.startedAtMs == 0 {
				s.startedAtMs = timestampMillis(partTimestamp, nowMillis)
			}
		}
	case "error":
		if errText := strings.TrimSpace(stringValue(part["errorText"])); errText != "" {
			s.errorText = errText
		}
		if s.completedAtMs == 0 {
			s.completedAtMs = timestampMillis(partTimestamp, nowMillis)
		}
	case "abort":
		s.finishReason = trimDefault(stringValue(part["reason"]), "aborted")
		if s.completedAtMs == 0 {
			s.completedAtMs = timestampMillis(partTimestamp, nowMillis)
		}
	case "finish":
		if finishReason := strings.TrimSpace(stringValue(part["finishReason"])); finishReason != "" {
			s.finishReason = finishReason
		}
		if errText := strings.TrimSpace(stringValue(part["errorText"])); errText != "" {
			s.errorText = errText
		}
		if s.completedAtMs == 0 {
			s.completedAtMs = timestampMillis(partTimestamp, nowMillis)
		}
	}
}

func (s *StreamPartState) applyPartTimestamp(partType string, ts time.Time) {
	if s == nil || ts.IsZero() {
		return
	}
	tsMillis := ts.UnixMilli()
	switch partType {
	case "start":
		if s.startedAtMs == 0 || tsMillis < s.startedAtMs {
			s.startedAtMs = tsMillis
		}
	case "text-delta", "reasoning-delta":
		if s.startedAtMs == 0 || tsMillis < s.startedAtMs {
			s.startedAtMs = tsMillis
		}
		if s.firstTokenAtMs == 0 || tsMillis < s.firstTokenAtMs {
			s.firstTokenAtMs = tsMillis
		}
	case "abort", "error", "finish":
		if s.completedAtMs == 0 || tsMillis > s.completedAtMs {
			s.completedAtMs = tsMillis
		}
	}
}

func (s *StreamPartState) VisibleText() string { return s.visible.String() }

func (s *StreamPartState) AccumulatedText() string { return s.accumulated.String() }

func (s *StreamPartState) LastVisibleText() string { return s.lastVisibleText }

func (s *StreamPartState) SetLastVisibleText(text string) {
	s.lastVisibleText = strings.TrimSpace(text)
}

func (s *StreamPartState) FinishReason() string { return s.finishReason }

func (s *StreamPartState) SetFinishReason(reason string) {
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		s.finishReason = trimmed
	}
}

func (s *StreamPartState) ErrorText() string { return s.errorText }

func (s *StreamPartState) SetErrorText(errText string) {
	if trimmed := strings.TrimSpace(errText); trimmed != "" {
		s.errorText = trimmed
	}
}

func (s *StreamPartState) StartedAtMs() int64 { return s.startedAtMs }

func (s *StreamPartState) SetStartedAtMs(v int64) {
	if v > 0 {
		s.startedAtMs = v
	}
}

func (s *StreamPartState) FirstTokenAtMs() int64 { return s.firstTokenAtMs }

func (s *StreamPartState) SetFirstTokenAtMs(v int64) {
	if v > 0 {
		s.firstTokenAtMs = v
	}
}

func (s *StreamPartState) CompletedAtMs() int64 { return s.completedAtMs }

func (s *StreamPartState) SetCompletedAtMs(v int64) {
	if v > 0 {
		s.completedAtMs = v
	}
}

func timestampMillis(ts time.Time, fallback int64) int64 {
	if !ts.IsZero() {
		return ts.UnixMilli()
	}
	return fallback
}

func trimDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
