package agentremote

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beeper/agentremote/turns"
)

// BaseStreamState provides the common stream session fields and lifecycle
// methods shared across bridges that use turns.
type BaseStreamState struct {
	StreamMu                  sync.Mutex
	StreamSessions            map[string]*turns.StreamSession
	StreamFallbackToDebounced atomic.Bool
	streamClosing             atomic.Bool
}

// InitStreamState initialises the StreamSessions map. Call this during client
// construction.
func (s *BaseStreamState) InitStreamState() {
	s.StreamSessions = make(map[string]*turns.StreamSession)
	s.streamClosing.Store(false)
}

func (s *BaseStreamState) BeginStreamShutdown() {
	s.streamClosing.Store(true)
}

func (s *BaseStreamState) ResetStreamShutdown() {
	s.streamClosing.Store(false)
}

func (s *BaseStreamState) IsStreamShuttingDown() bool {
	return s.streamClosing.Load()
}

// CloseAllSessions ends every active stream session and clears the map.
func (s *BaseStreamState) CloseAllSessions() {
	s.BeginStreamShutdown()
	s.StreamMu.Lock()
	sessions := make([]*turns.StreamSession, 0, len(s.StreamSessions))
	for _, sess := range s.StreamSessions {
		if sess != nil {
			sessions = append(sessions, sess)
		}
	}
	s.StreamSessions = make(map[string]*turns.StreamSession)
	s.StreamMu.Unlock()
	for _, sess := range sessions {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		sess.End(ctx, turns.EndReasonDisconnect)
		cancel()
	}
}
