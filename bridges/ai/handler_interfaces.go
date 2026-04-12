package ai

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
)

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
