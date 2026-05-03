package ai

import (
	"context"
	"sync"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/sdk"
)

// Ack reaction tracking for removal after reply
// Maps room ID -> source message ID -> ack reaction metadata
const (
	ackReactionTTL             = 5 * time.Minute
	ackReactionCleanupInterval = time.Minute
)

type ackReactionEntry struct {
	targetNetworkID networkid.MessageID // Network ID of the target message for reaction removal
	emoji           string              // Emoji used for the reaction
	storedAt        time.Time
}

var (
	ackReactionStore   = make(map[id.RoomID]map[id.EventID]ackReactionEntry)
	ackReactionStoreMu sync.Mutex
	ackCleanupStop     = make(chan struct{})
)

func init() {
	go cleanupAckReactionStore()
}

func cleanupAckReactionStore() {
	ticker := time.NewTicker(ackReactionCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-ackReactionTTL)
			ackReactionStoreMu.Lock()
			for roomID, roomReactions := range ackReactionStore {
				for sourceEventID, entry := range roomReactions {
					if entry.storedAt.Before(cutoff) {
						delete(roomReactions, sourceEventID)
					}
				}
				if len(roomReactions) == 0 {
					delete(ackReactionStore, roomID)
				}
			}
			ackReactionStoreMu.Unlock()
		case <-ackCleanupStop:
			return
		}
	}
}

// sendAckReaction sends an acknowledgement reaction to a message via QueueRemoteEvent.
// Returns the event ID of the reaction for potential removal.
func (oc *AIClient) sendAckReaction(ctx context.Context, portal *bridgev2.Portal, targetEventID id.EventID, emoji string) id.EventID {
	if portal == nil || portal.MXID == "" || targetEventID == "" || emoji == "" {
		return ""
	}

	targetPart, err := oc.loadPortalMessagePartByMXID(ctx, portal, targetEventID)
	if err != nil || targetPart == nil {
		oc.loggerForContext(ctx).Warn().Err(err).Stringer("target_event", targetEventID).Msg("Target message not found for ack reaction")
		return ""
	}

	sender := oc.senderForPortal(ctx, portal)
	emojiID := networkid.EmojiID(emoji)
	result := oc.UserLogin.QueueRemoteEvent(sdk.BuildReactionEvent(
		portal.PortalKey,
		sender,
		targetPart.ID,
		emoji,
		emojiID,
		time.Now(),
		0,
		"ai_reaction_target",
		nil,
		nil,
	))
	if !result.Success {
		oc.loggerForContext(ctx).Warn().
			Stringer("target_event", targetEventID).
			Str("emoji", emoji).
			Msg("Failed to send ack reaction")
		return ""
	}

	oc.loggerForContext(ctx).Debug().
		Stringer("target_event", targetEventID).
		Str("emoji", emoji).
		Msg("Sent ack reaction")
	return result.EventID
}

// storeAckReaction stores an ack reaction for later removal.
func (oc *AIClient) storeAckReaction(ctx context.Context, portal *bridgev2.Portal, sourceEventID id.EventID, emoji string) {
	if portal == nil || portal.MXID == "" {
		return
	}
	// Look up the network message ID for the source event
	var targetNetworkID networkid.MessageID
	if part, err := oc.loadPortalMessagePartByMXID(ctx, portal, sourceEventID); err == nil && part != nil {
		targetNetworkID = part.ID
	}

	ackReactionStoreMu.Lock()
	defer ackReactionStoreMu.Unlock()

	if ackReactionStore[portal.MXID] == nil {
		ackReactionStore[portal.MXID] = make(map[id.EventID]ackReactionEntry)
	}
	ackReactionStore[portal.MXID][sourceEventID] = ackReactionEntry{
		targetNetworkID: targetNetworkID,
		emoji:           emoji,
		storedAt:        time.Now(),
	}
}

// removeAckReaction removes a previously sent ack reaction via bridgev2's pipeline.
func (oc *AIClient) removeAckReaction(ctx context.Context, portal *bridgev2.Portal, sourceEventID id.EventID) {
	ackReactionStoreMu.Lock()
	roomReactions := ackReactionStore[portal.MXID]
	if roomReactions == nil {
		ackReactionStoreMu.Unlock()
		return
	}
	entry, ok := roomReactions[sourceEventID]
	if !ok {
		ackReactionStoreMu.Unlock()
		return
	}
	delete(roomReactions, sourceEventID)
	ackReactionStoreMu.Unlock()

	if entry.targetNetworkID == "" || entry.emoji == "" {
		return
	}

	sender := oc.senderForPortal(ctx, portal)
	oc.UserLogin.QueueRemoteEvent(sdk.BuildReactionRemoveEvent(
		portal.PortalKey,
		sender,
		entry.targetNetworkID,
		networkid.EmojiID(entry.emoji),
		time.Now(),
		0,
		"ai_reaction_remove_target",
	))

	oc.loggerForContext(ctx).Debug().
		Stringer("source_event", sourceEventID).
		Str("emoji", entry.emoji).
		Msg("Queued ack reaction removal")
}
