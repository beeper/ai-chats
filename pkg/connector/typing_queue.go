package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"
)

func (oc *AIClient) startQueueTyping(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, typingCtx *TypingContext) {
	if oc == nil || portal == nil || portal.MXID == "" {
		return
	}
	if meta != nil && normalizeSendPolicyMode(meta.SendPolicy) == "deny" {
		return
	}
	if typingCtx == nil {
		typingCtx = &TypingContext{IsGroup: oc.isGroupChat(ctx, portal)}
	}
	mode := oc.resolveTypingMode(meta, typingCtx, false)
	if mode != TypingModeInstant {
		return
	}
	interval := oc.resolveTypingInterval(meta)
	if interval <= 0 {
		return
	}

	roomID := portal.MXID
	oc.queueTypingMu.Lock()
	existing := oc.queueTyping[roomID]
	if existing == nil {
		backgroundCtx := oc.backgroundContext(ctx)
		controller := NewTypingController(oc, backgroundCtx, portal, TypingControllerOptions{
			Interval: interval,
			TTL:      typingTTL,
		})
		oc.queueTyping[roomID] = controller
		oc.queueTypingMu.Unlock()
		controller.Start()
		return
	}
	oc.queueTypingMu.Unlock()
	existing.RefreshTTL()
}

func (oc *AIClient) stopQueueTyping(roomID id.RoomID) {
	if oc == nil || roomID == "" {
		return
	}
	oc.queueTypingMu.Lock()
	controller := oc.queueTyping[roomID]
	if controller != nil {
		delete(oc.queueTyping, roomID)
	}
	oc.queueTypingMu.Unlock()
	if controller != nil {
		controller.Stop()
	}
}
