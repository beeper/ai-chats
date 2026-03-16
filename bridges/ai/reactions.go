package ai

import (
	"context"
	"time"

	"go.mau.fi/util/variationselector"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
)

func (oc *AIClient) sendReaction(ctx context.Context, portal *bridgev2.Portal, targetEventID id.EventID, emoji string) {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.ID == "" || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.DB == nil || portal == nil || portal.MXID == "" || targetEventID == "" || emoji == "" {
		return
	}

	// Look up the target message by Matrix event ID to get the network message ID.
	targetPart, err := oc.UserLogin.Bridge.DB.Message.GetPartByMXID(ctx, targetEventID)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).
			Stringer("target_event", targetEventID).
			Msg("Failed to load reaction target from database")
		return
	}
	if targetPart == nil {
		oc.loggerForContext(ctx).Warn().
			Stringer("target_event", targetEventID).
			Msg("Reaction target message not found in database")
		return
	}
	if targetPart.Room != portal.PortalKey {
		oc.loggerForContext(ctx).Warn().
			Stringer("target_event", targetEventID).
			Msg("Reaction target message is not in the current portal")
		return
	}

	senderID := oc.reactionSenderID(ctx, portal)
	if senderID == "" {
		oc.loggerForContext(ctx).Warn().
			Stringer("target_event", targetEventID).
			Msg("Failed to resolve reaction sender ID")
		return
	}

	normalizedEmoji := variationselector.Remove(emoji)
	oc.UserLogin.QueueRemoteEvent(agentremote.BuildReactionEvent(
		portal.PortalKey,
		bridgev2.EventSender{Sender: senderID, SenderLogin: oc.UserLogin.ID},
		targetPart.ID,
		normalizedEmoji,
		networkid.EmojiID(normalizedEmoji),
		time.Now(),
		0,
		"ai_reaction_target",
		nil,
		nil,
	))
}

func (oc *AIClient) reactionSenderID(_ context.Context, portal *bridgev2.Portal) networkid.UserID {
	if portal == nil || oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return ""
	}
	meta := portalMeta(portal)
	agentID := resolveAgentID(meta)
	modelID := oc.effectiveModel(meta)
	if agentID != "" {
		return oc.agentUserID(agentID)
	}
	return modelUserID(modelID)
}
