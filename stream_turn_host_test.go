package agentremote

import (
	"testing"
	"time"
)

type testHostAborterState struct {
	id      string
	aborted string
}

func (s *testHostAborterState) Abort(reason string) {
	s.aborted = reason
}

func TestStreamTurnHostDrainAndAbortGetsAbortersOutsideLock(t *testing.T) {
	var host *StreamTurnHost[testHostAborterState]
	state := &testHostAborterState{id: "turn-1"}
	host = NewStreamTurnHost(StreamTurnHostCallbacks[testHostAborterState]{
		GetAborter: func(state *testHostAborterState) Aborter {
			_ = host.IsActive(state.id)
			return state
		},
	})
	host.Lock()
	host.SetLocked(state.id, state)
	host.Unlock()

	done := make(chan struct{})
	go func() {
		host.DrainAndAbort("disconnect")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("DrainAndAbort blocked while collecting aborters")
	}

	if state.aborted != "disconnect" {
		t.Fatalf("expected abort reason to propagate, got %q", state.aborted)
	}
	if host.IsActive(state.id) {
		t.Fatal("expected state to be removed after drain")
	}
}
