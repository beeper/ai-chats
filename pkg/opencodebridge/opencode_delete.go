package opencodebridge

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
)

// HandleMatrixDeleteChat deletes the remote OpenCode session when a chat is deleted.
func (b *Bridge) HandleMatrixDeleteChat(ctx context.Context, msg *bridgev2.MatrixDeleteChat) error {
	if b == nil || msg == nil || msg.Portal == nil {
		return nil
	}
	meta := b.portalMeta(msg.Portal)
	if meta == nil || !meta.IsOpenCodeRoom {
		// Allow deletion for non-OpenCode rooms without remote cleanup.
		return nil
	}
	if b.manager == nil {
		return nil
	}
	return b.manager.DeleteSession(ctx, meta.InstanceID, meta.SessionID)
}
