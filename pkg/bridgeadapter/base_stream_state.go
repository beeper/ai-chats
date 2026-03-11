package bridgeadapter

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/beeper/agentremote/pkg/shared/streamtransport"
)

// BaseStreamState provides the common stream session fields and lifecycle
// methods shared across bridges that use streamtransport.
type BaseStreamState struct {
	StreamMu                  sync.Mutex
	StreamSessions            map[string]*streamtransport.StreamSession
	StreamFallbackToDebounced atomic.Bool
	streamClosing             atomic.Bool
}

// InitStreamState initialises the StreamSessions map. Call this during client
// construction.
func (s *BaseStreamState) InitStreamState() {
	s.StreamSessions = make(map[string]*streamtransport.StreamSession)
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
	s.streamClosing.Store(true)
	s.StreamMu.Lock()
	sessions := make([]*streamtransport.StreamSession, 0, len(s.StreamSessions))
	for _, sess := range s.StreamSessions {
		if sess != nil {
			sessions = append(sessions, sess)
		}
	}
	s.StreamSessions = make(map[string]*streamtransport.StreamSession)
	s.StreamMu.Unlock()
	for _, sess := range sessions {
		sess.End(context.Background(), streamtransport.EndReasonDisconnect)
	}
}
