package connector

import (
	"context"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/textfs"
)

func (m *MemorySearchManager) warmSession(ctx context.Context, sessionKey string) {
	if m == nil || m.cfg == nil {
		return
	}
	if !m.cfg.Sync.OnSessionStart {
		return
	}
	key := strings.TrimSpace(sessionKey)
	if key != "" {
		m.mu.Lock()
		if m.sessionWarm == nil {
			m.sessionWarm = make(map[string]struct{})
		}
		if _, ok := m.sessionWarm[key]; ok {
			m.mu.Unlock()
			return
		}
		m.sessionWarm[key] = struct{}{}
		m.mu.Unlock()
	}

	m.log.Debug().Str("session", key).Msg("Memory sync warming session")
	go func() {
		if err := m.sync(context.Background(), key, false); err != nil {
			m.log.Warn().Str("session", key).Msg("memory sync failed (session-start): " + err.Error())
		} else {
			m.log.Debug().Str("session", key).Msg("Memory sync session warm complete")
		}
	}()
}

func (m *MemorySearchManager) notifyFileChanged(path string) {
	if m == nil || m.cfg == nil {
		return
	}
	normalized, err := textfs.NormalizePath(path)
	if err != nil {
		return
	}
	if !isAllowedMemoryPath(normalized, m.cfg.ExtraPaths) {
		return
	}
	m.mu.Lock()
	m.dirty = true
	m.mu.Unlock()
	m.scheduleWatchSync()
}

func (m *MemorySearchManager) scheduleWatchSync() {
	if m == nil || m.cfg == nil || !m.cfg.Sync.Watch {
		return
	}
	delay := time.Duration(m.cfg.Sync.WatchDebounceMs) * time.Millisecond
	if delay <= 0 {
		delay = time.Duration(1500) * time.Millisecond
	}
	m.log.Debug().Dur("delay", delay).Msg("Memory sync scheduling watch sync")
	m.mu.Lock()
	if m.watchTimer != nil {
		m.watchTimer.Stop()
	}
	m.watchTimer = time.AfterFunc(delay, func() {
		m.mu.Lock()
		m.watchTimer = nil
		m.mu.Unlock()
		if err := m.sync(context.Background(), "", false); err != nil {
			m.log.Warn().Msg("memory sync failed (watch): " + err.Error())
		} else {
			m.log.Debug().Msg("Memory sync watch complete")
		}
	})
	m.mu.Unlock()
}

func (m *MemorySearchManager) ensureIntervalSync() {
	if m == nil || m.cfg == nil {
		return
	}
	if m.cfg.Sync.IntervalMinutes <= 0 {
		return
	}
	m.intervalOnce.Do(func() {
		interval := time.Duration(m.cfg.Sync.IntervalMinutes) * time.Minute
		m.mu.Lock()
		if m.intervalStop == nil {
			m.intervalStop = make(chan struct{})
		}
		stopCh := m.intervalStop
		m.mu.Unlock()
		m.log.Debug().Dur("interval", interval).Msg("Memory sync starting interval sync goroutine")
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if err := m.sync(context.Background(), "", false); err != nil {
						m.log.Warn().Msg("memory sync failed (interval): " + err.Error())
					} else {
						m.log.Debug().Msg("Memory sync interval complete")
					}
				case <-stopCh:
					return
				}
			}
		}()
	})
}

func notifyMemoryFileChanged(ctx context.Context, path string) {
	btc := GetBridgeToolContext(ctx)
	if btc == nil || btc.Client == nil {
		return
	}
	meta := portalMeta(btc.Portal)
	agentID := resolveAgentID(meta)
	manager, _ := getMemorySearchManager(btc.Client, agentID)
	if manager == nil {
		return
	}
	manager.notifyFileChanged(path)
}
