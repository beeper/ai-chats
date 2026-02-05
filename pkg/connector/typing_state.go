package connector

import (
	"time"

	"maunium.net/go/mautrix/id"
)

type userTypingState struct {
	isTyping     bool
	lastActivity time.Time
}

func (oc *AIClient) noteUserActivity(roomID id.RoomID) {
	if oc == nil || roomID == "" {
		return
	}
	oc.userTypingMu.Lock()
	state := oc.userTypingState[roomID]
	state.isTyping = false
	state.lastActivity = time.Now()
	oc.userTypingState[roomID] = state
	oc.userTypingMu.Unlock()
}

func (oc *AIClient) setUserTyping(roomID id.RoomID, isTyping bool) {
	if oc == nil || roomID == "" {
		return
	}
	oc.userTypingMu.Lock()
	state := oc.userTypingState[roomID]
	state.isTyping = isTyping
	state.lastActivity = time.Now()
	oc.userTypingState[roomID] = state
	oc.userTypingMu.Unlock()
}

func (oc *AIClient) getUserTypingState(roomID id.RoomID) (userTypingState, bool) {
	if oc == nil || roomID == "" {
		return userTypingState{}, false
	}
	oc.userTypingMu.Lock()
	state, ok := oc.userTypingState[roomID]
	oc.userTypingMu.Unlock()
	return state, ok
}

func (oc *AIClient) isUserTyping(roomID id.RoomID) bool {
	state, ok := oc.getUserTypingState(roomID)
	if !ok {
		return false
	}
	return state.isTyping
}

func (oc *AIClient) userIdleFor(roomID id.RoomID, d time.Duration) bool {
	state, ok := oc.getUserTypingState(roomID)
	if !ok || state.lastActivity.IsZero() {
		return true
	}
	return time.Since(state.lastActivity) >= d
}
