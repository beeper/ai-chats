package ai

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"
)

// HandleMatrixDeleteChat best-effort cleans up AI-room runtime and persisted
// state when Matrix deletes the chat. The core bridge handles the actual room
// and portal deletion.
func (oc *AIClient) HandleMatrixDeleteChat(ctx context.Context, msg *bridgev2.MatrixDeleteChat) error {
	if oc == nil || msg == nil || msg.Portal == nil {
		return nil
	}

	portal := msg.Portal
	meta := portalMeta(portal)
	roomID := portal.MXID
	sessionKey := strings.TrimSpace(roomID.String())

	if roomID != "" {
		oc.cleanupDeletedRoomRuntime(ctx, roomID)
	}
	if sessionKey != "" {
		oc.deletePersistedSessionArtifacts(ctx, sessionKey)
	}

	if meta != nil {
		oc.notifySessionMutation(ctx, portal, meta, false)
	}
	return nil
}

func (oc *AIClient) cleanupDeletedRoomRuntime(ctx context.Context, roomID id.RoomID) {
	if oc == nil || roomID == "" {
		return
	}

	// Room deletion should be silent; drop queued work instead of sending stop
	// notices/status events into a room that's being removed.
	_ = oc.drainPendingQueue(roomID)
	oc.stopSubagentRuns(ctx, roomID)
	oc.stopQueueTyping(roomID)
	oc.releaseRoom(roomID)

	oc.groupHistoryMu.Lock()
	delete(oc.groupHistoryBuffers, roomID)
	oc.groupHistoryMu.Unlock()

	oc.userTypingMu.Lock()
	delete(oc.userTypingState, roomID)
	oc.userTypingMu.Unlock()

	ackReactionStoreMu.Lock()
	delete(ackReactionStore, roomID)
	ackReactionStoreMu.Unlock()
}

func (oc *AIClient) deletePersistedSessionArtifacts(ctx context.Context, sessionKey string) {
	if oc == nil {
		return
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}

	db, bridgeID, loginID := loginDBContext(oc)
	if db != nil && bridgeID != "" && loginID != "" {
		bestEffortExec(ctx, db, oc.Log(),
			`DELETE FROM agentremote_sessions WHERE bridge_id=$1 AND login_id=$2 AND session_key=$3`,
			bridgeID, loginID, sessionKey,
		)
		bestEffortExec(ctx, db, oc.Log(),
			`DELETE FROM aichats_system_events WHERE bridge_id=$1 AND login_id=$2 AND session_key=$3`,
			bridgeID, loginID, sessionKey,
		)
	}
	deleteInternalPromptsForRoom(ctx, oc, id.RoomID(sessionKey))

	clearSystemEventsForSession(systemEventsOwnerKey(oc), sessionKey)
}
