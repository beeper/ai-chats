package agentremote

import (
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/variationselector"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
)

func reactionEventMeta(
	eventType bridgev2.RemoteEventType,
	portal networkid.PortalKey,
	sender bridgev2.EventSender,
	targetMessage networkid.MessageID,
	logKey string,
	timing EventTiming,
) simplevent.EventMeta {
	return simplevent.EventMeta{
		Type:        eventType,
		PortalKey:   portal,
		Sender:      sender,
		Timestamp:   timing.Timestamp,
		StreamOrder: timing.StreamOrder,
		LogContext: func(c zerolog.Context) zerolog.Context {
			return c.Str(logKey, string(targetMessage))
		},
	}
}

// BuildReactionEvent creates a reaction add event with normalized emoji data.
func BuildReactionEvent(
	portal networkid.PortalKey,
	sender bridgev2.EventSender,
	targetMessage networkid.MessageID,
	emoji string,
	emojiID networkid.EmojiID,
	timestamp time.Time,
	streamOrder int64,
	logKey string,
	dbMeta *database.Reaction,
	extraContent map[string]any,
) *simplevent.Reaction {
	normalized := variationselector.Remove(emoji)
	if normalized == "" {
		normalized = variationselector.Remove(string(emojiID))
	}
	if emojiID == "" {
		emojiID = networkid.EmojiID(normalized)
	}
	timing := ResolveEventTiming(timestamp, streamOrder)
	return &simplevent.Reaction{
		EventMeta:      reactionEventMeta(bridgev2.RemoteEventReaction, portal, sender, targetMessage, logKey, timing),
		TargetMessage:  targetMessage,
		Emoji:          normalized,
		EmojiID:        emojiID,
		ReactionDBMeta: dbMeta,
		ExtraContent:   extraContent,
	}
}

// BuildReactionRemoveEvent creates a reaction removal event with explicit timing.
func BuildReactionRemoveEvent(
	portal networkid.PortalKey,
	sender bridgev2.EventSender,
	targetMessage networkid.MessageID,
	emojiID networkid.EmojiID,
	timestamp time.Time,
	streamOrder int64,
	logKey string,
) *simplevent.Reaction {
	timing := ResolveEventTiming(timestamp, streamOrder)
	return &simplevent.Reaction{
		EventMeta:     reactionEventMeta(bridgev2.RemoteEventReactionRemove, portal, sender, targetMessage, logKey, timing),
		TargetMessage: targetMessage,
		EmojiID:       emojiID,
	}
}
