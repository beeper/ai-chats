package streamtransport

import (
	"strings"
	"sync/atomic"
)

// ResolveModeWithRuntimeFallback resolves configured stream mode and applies
// a process-local fallback switch when ephemeral transport is known unsupported.
func ResolveModeWithRuntimeFallback(configValue string, fallbackToDebounced *atomic.Bool) Mode {
	mode := ResolveMode(configValue)
	if mode == ModeDebouncedEdit {
		return mode
	}
	if fallbackToDebounced != nil && fallbackToDebounced.Load() {
		return ModeDebouncedEdit
	}
	return mode
}

// EnableRuntimeFallbackToDebounced flips the process-local ephemeral fallback flag.
// Returns true only on the first successful switch.
func EnableRuntimeFallbackToDebounced(fallbackToDebounced *atomic.Bool) bool {
	if fallbackToDebounced == nil {
		return false
	}
	return fallbackToDebounced.CompareAndSwap(false, true)
}

// HandleDebouncedPart routes stream part types to debounced edit callbacks.
func HandleDebouncedPart(part map[string]any, send func(force bool), clearTurnGate func()) {
	if send == nil {
		return
	}
	partType, _ := part["type"].(string)
	switch strings.TrimSpace(partType) {
	case "text-delta", "reasoning-delta", "text-end", "reasoning-end":
		send(false)
	case "finish", "abort", "error":
		send(true)
		if clearTurnGate != nil {
			clearTurnGate()
		}
	}
}
