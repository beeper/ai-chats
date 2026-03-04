package memory

import (
	"strings"
)

// Close stops background timers/goroutines. It is safe to call multiple times.
func (m *MemorySearchManager) Close() {
	if m == nil {
		return
	}

	// Stop interval ticker goroutine (if started).
	if m.closeIntervalStop != nil {
		m.closeIntervalStop()
	}

	// Stop debounced timers.
	m.mu.Lock()
	if m.watchTimer != nil {
		m.watchTimer.Stop()
		m.watchTimer = nil
	}
	if m.sessionWatchTimer != nil {
		m.sessionWatchTimer.Stop()
		m.sessionWatchTimer = nil
	}
	m.mu.Unlock()
}

// StopManagersForLogin stops and removes all memory managers for a login.
// Used during disconnect and purge to release goroutines and timers.
func StopManagersForLogin(bridgeID, loginID string) {
	if strings.TrimSpace(bridgeID) == "" || strings.TrimSpace(loginID) == "" {
		return
	}
	prefix := bridgeID + ":" + loginID + ":"

	memoryManagerCache.mu.Lock()
	managers := make([]*MemorySearchManager, 0, len(memoryManagerCache.managers))
	for key, mgr := range memoryManagerCache.managers {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if mgr != nil {
			managers = append(managers, mgr)
		}
		delete(memoryManagerCache.managers, key)
	}
	memoryManagerCache.mu.Unlock()

	for _, mgr := range managers {
		mgr.Close()
	}
}
