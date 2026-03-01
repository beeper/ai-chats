package memory

import (
	"context"
	"strings"
)

// Close stops background timers/goroutines. It is safe to call multiple times.
// Vector connections are no longer held persistently (grab+release per operation),
// so there is nothing to release here.
func (m *MemorySearchManager) Close() {
	if m == nil {
		return
	}

	// Stop interval ticker goroutine (if started).
	m.mu.Lock()
	stopCh := m.intervalStop
	m.mu.Unlock()
	if stopCh != nil {
		m.intervalStopOnce.Do(func() {
			close(stopCh)
		})
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

func PurgeManagersForLogin(ctx context.Context, bridgeID, loginID string, chunkIDsByAgent map[string][]string) {
	if strings.TrimSpace(bridgeID) == "" || strings.TrimSpace(loginID) == "" {
		return
	}
	_ = chunkIDsByAgent
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

	if ctx == nil {
		ctx = context.Background()
	}
	for _, mgr := range managers {
		mgr.Close()
	}
}

// stopMemoryManagersForLogin stops all memory managers for a login.
// Used during disconnect to release goroutines and timers.
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
