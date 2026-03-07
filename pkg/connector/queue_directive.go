package connector

import (
	"fmt"
	"strings"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
)

type queueDirective struct {
	Cleaned      string
	QueueMode    airuntime.QueueMode
	QueueReset   bool
	RawMode      string
	DebounceMs   *int
	Cap          *int
	DropPolicy   *airuntime.QueueDropPolicy
	RawDebounce  string
	RawCap       string
	RawDrop      string
	HasDirective bool
	HasOptions   bool
	HasDebounce  bool
	HasCap       bool
	HasDrop      bool
}

func parseQueueDebounce(raw string) *int {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parsed, err := parseDurationMs(raw, "ms")
	if err != nil {
		return nil
	}
	value := int(parsed)
	if value < 0 {
		return nil
	}
	return &value
}

func parseQueueCap(raw string) *int {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	value := 0
	if _, err := fmt.Sscanf(trimmed, "%d", &value); err != nil {
		return nil
	}
	if value < 1 {
		return nil
	}
	return &value
}

func parseQueueDirectiveArgs(raw string) (consumed int, result queueDirective) {
	i := 0
	for i < len(raw) && raw[i] <= ' ' {
		i++
	}
	if i < len(raw) && raw[i] == ':' {
		i++
		for i < len(raw) && raw[i] <= ' ' {
			i++
		}
	}
	consumed = i
	takeToken := func() string {
		if i >= len(raw) {
			return ""
		}
		start := i
		for i < len(raw) && raw[i] > ' ' {
			i++
		}
		token := raw[start:i]
		for i < len(raw) && raw[i] <= ' ' {
			i++
		}
		if token == "" {
			return ""
		}
		consumed = i
		return token
	}

	for i < len(raw) {
		token := takeToken()
		if token == "" {
			break
		}
		lowered := strings.ToLower(strings.TrimSpace(token))
		if lowered == "reset" {
			result.QueueReset = true
			break
		}
		if strings.HasPrefix(lowered, "debounce:") {
			parts := strings.SplitN(token, ":", 2)
			if len(parts) > 1 {
				result.RawDebounce = parts[1]
				result.DebounceMs = parseQueueDebounce(parts[1])
				result.HasOptions = true
				result.HasDebounce = true
			}
			continue
		}
		if strings.HasPrefix(lowered, "cap:") {
			parts := strings.SplitN(token, ":", 2)
			if len(parts) > 1 {
				result.RawCap = parts[1]
				result.Cap = parseQueueCap(parts[1])
				result.HasOptions = true
				result.HasCap = true
			}
			continue
		}
		if strings.HasPrefix(lowered, "drop:") {
			parts := strings.SplitN(token, ":", 2)
			if len(parts) > 1 {
				result.RawDrop = parts[1]
				if policy, ok := airuntime.NormalizeQueueDropPolicy(parts[1]); ok {
					result.DropPolicy = &policy
				}
				result.HasOptions = true
				result.HasDrop = true
			}
			continue
		}
		if mode, ok := airuntime.NormalizeQueueMode(token); ok {
			result.QueueMode = mode
			result.RawMode = token
			continue
		}
		break
	}
	return consumed, result
}

// NOTE: Slash-style inline `/queue ...` directives are intentionally not supported.
