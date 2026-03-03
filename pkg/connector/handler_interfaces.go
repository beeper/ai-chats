package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
)

// HandleMatrixReadReceipt tracks read receipt positions. AI-bridge is the
// authoritative side so there is nothing to forward to a remote network.
func (oc *AIClient) HandleMatrixReadReceipt(ctx context.Context, msg *bridgev2.MatrixReadReceipt) error {
	return nil
}

// HandleMatrixRoomName handles room rename events from Matrix.
// Returns true to indicate the name change was accepted (no remote to forward to).
func (oc *AIClient) HandleMatrixRoomName(ctx context.Context, msg *bridgev2.MatrixRoomName) (bool, error) {
	return true, nil
}

// HandleMatrixRoomTopic handles room topic change events from Matrix.
// Returns true to indicate the topic change was accepted.
func (oc *AIClient) HandleMatrixRoomTopic(ctx context.Context, msg *bridgev2.MatrixRoomTopic) (bool, error) {
	return true, nil
}

// HandleMatrixRoomAvatar handles room avatar change events from Matrix.
// Returns true to indicate the avatar change was accepted.
func (oc *AIClient) HandleMatrixRoomAvatar(ctx context.Context, msg *bridgev2.MatrixRoomAvatar) (bool, error) {
	return true, nil
}

// HandleMute tracks mute state for portals. No remote forwarding needed.
func (oc *AIClient) HandleMute(ctx context.Context, msg *bridgev2.MatrixMute) error {
	return nil
}

// HandleMarkedUnread tracks unread state for portals. No remote forwarding needed.
func (oc *AIClient) HandleMarkedUnread(ctx context.Context, msg *bridgev2.MatrixMarkedUnread) error {
	return nil
}
