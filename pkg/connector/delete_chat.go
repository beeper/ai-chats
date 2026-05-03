package connector

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-chats/pkg/shared/aihelpers"
)

// HandleMatrixDeleteChat best-effort cleans up AI-room runtime and persisted
// state when Matrix deletes the chat. The core bridge handles the actual room
// and portal deletion.
func (oc *AIClient) HandleMatrixDeleteChat(ctx context.Context, msg *bridgev2.MatrixDeleteChat) error {
	if oc == nil || msg == nil || msg.Portal == nil {
		return nil
	}

	portal := msg.Portal
	roomID := portal.MXID

	if roomID != "" {
		oc.cleanupDeletedRoomRuntime(ctx, roomID)
	}
	oc.deletePersistedRoomArtifacts(ctx, portal)
	if err := aihelpers.DeleteConversationState(ctx, portal); err != nil {
		oc.log.Warn().Err(err).Str("portal_id", string(portal.PortalKey.ID)).Msg("failed to delete AIHelper conversation state")
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
	oc.stopQueueTyping(roomID)
	oc.releaseRoom(roomID)

	oc.groupHistoryMu.Lock()
	delete(oc.groupHistoryBuffers, roomID)
	oc.groupHistoryMu.Unlock()
}

func (oc *AIClient) deletePersistedRoomArtifacts(ctx context.Context, portal *bridgev2.Portal) {
	if oc == nil || portal == nil {
		return
	}

	scope := loginScopeForClient(oc)
	if scope != nil && scope.loginID != "" {
		execDelete(ctx, scope.db, &oc.log,
			`DELETE FROM `+aiPortalStateTable+` WHERE bridge_id=$1 AND portal_id=$2 AND portal_receiver=$3`,
			scope.bridgeID, strings.TrimSpace(string(portal.PortalKey.ID)), strings.TrimSpace(string(portal.PortalKey.Receiver)),
		)
	}
	deleteAITurnsForPortal(ctx, portal)
}
