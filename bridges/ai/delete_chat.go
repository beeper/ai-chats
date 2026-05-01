package ai

import (
	"context"
	"strings"

	"github.com/beeper/agentremote/sdk"
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
		oc.deletePersistedSessionArtifacts(ctx, portal, sessionKey)
	}
	if err := sdk.DeleteConversationState(ctx, portal); err != nil {
		oc.log.Warn().Err(err).Str("portal_id", string(portal.PortalKey.ID)).Msg("failed to delete SDK conversation state")
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

	ackReactionStoreMu.Lock()
	delete(ackReactionStore, roomID)
	ackReactionStoreMu.Unlock()
}

func (oc *AIClient) deletePersistedSessionArtifacts(ctx context.Context, portal *bridgev2.Portal, sessionKey string) {
	if oc == nil {
		return
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}

	scope := loginScopeForClient(oc)
	if scope != nil && scope.loginID != "" {
		execDelete(ctx, scope.db, &oc.log,
			`DELETE FROM `+aiSessionsTable+` WHERE bridge_id=$1 AND login_id=$2 AND session_key=$3`,
			scope.bridgeID, scope.loginID, sessionKey,
		)
		execDelete(ctx, scope.db, &oc.log,
			`DELETE FROM `+aiSystemEventsTable+` WHERE bridge_id=$1 AND login_id=$2 AND session_key=$3`,
			scope.bridgeID, scope.loginID, sessionKey,
		)
		execDelete(ctx, scope.db, &oc.log,
			`DELETE FROM `+aiPortalStateTable+` WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3`,
			scope.bridgeID, strings.TrimSpace(string(portal.PortalKey.ID)), strings.TrimSpace(string(portal.PortalKey.Receiver)),
		)
	}
	deleteAITurnsForPortal(ctx, portal)

	clearSystemEventsForSession(systemEventsOwnerKey(oc), sessionKey)
}
