package connector

import (
	"context"
	"errors"
	"time"

	"go.mau.fi/util/variationselector"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// getMatrixConnector returns the Matrix connector interface from the bridge.
func getMatrixConnector(btc *BridgeToolContext) bridgev2.MatrixConnector {
	if btc == nil || btc.Client == nil || btc.Client.UserLogin == nil || btc.Client.UserLogin.Bridge == nil {
		return nil
	}
	return btc.Client.UserLogin.Bridge.Matrix
}

// MatrixReactionSummary represents a summary of reactions on a message.
type MatrixReactionSummary struct {
	Key   string   `json:"key"`   // The emoji
	Count int      `json:"count"` // Number of reactions with this emoji
	Users []string `json:"users"` // User IDs who reacted
}

// listMatrixReactions lists all reactions on a message using the bridge database.
func listMatrixReactions(ctx context.Context, btc *BridgeToolContext, eventID id.EventID) ([]MatrixReactionSummary, error) {
	if btc == nil || btc.Client == nil || btc.Client.UserLogin == nil || btc.Client.UserLogin.Bridge == nil || btc.Portal == nil {
		return nil, nil
	}

	targetPart, err := btc.Client.UserLogin.Bridge.DB.Message.GetPartByMXID(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if targetPart == nil {
		return nil, errors.New("target message not found")
	}

	receiver := btc.Portal.Receiver
	if receiver == "" {
		receiver = btc.Client.UserLogin.ID
	}

	reactions, err := btc.Client.UserLogin.Bridge.DB.Reaction.GetAllToMessagePart(
		ctx,
		receiver,
		targetPart.ID,
		targetPart.PartID,
	)
	if err != nil {
		return nil, err
	}

	summaries := make(map[string]*MatrixReactionSummary)
	for _, reaction := range reactions {
		emoji := reaction.Emoji
		if emoji == "" {
			emoji = string(reaction.EmojiID)
		}
		if emoji == "" {
			continue
		}

		sender := reaction.SenderMXID.String()
		if sender == "" {
			sender = string(reaction.SenderID)
		}

		if summaries[emoji] == nil {
			summaries[emoji] = &MatrixReactionSummary{Key: emoji, Count: 0, Users: []string{}}
		}
		summaries[emoji].Count++
		summaries[emoji].Users = append(summaries[emoji].Users, sender)
	}

	result := make([]MatrixReactionSummary, 0, len(summaries))
	for _, summary := range summaries {
		result = append(result, *summary)
	}
	return result, nil
}

// removeMatrixReactions removes the bot's reactions from a message using the bridge database.
// If emoji is specified, only removes that specific reaction.
// If emoji is empty, removes all of the bot's reactions.
func removeMatrixReactions(ctx context.Context, btc *BridgeToolContext, eventID id.EventID, emoji string) (int, error) {
	if btc == nil || btc.Client == nil || btc.Client.UserLogin == nil || btc.Client.UserLogin.Bridge == nil || btc.Portal == nil {
		return 0, nil
	}

	intent := btc.Client.getModelIntent(ctx, btc.Portal)
	if intent == nil {
		return 0, errors.New("failed to get model intent")
	}

	targetPart, err := btc.Client.UserLogin.Bridge.DB.Message.GetPartByMXID(ctx, eventID)
	if err != nil {
		return 0, err
	}
	if targetPart == nil {
		return 0, errors.New("target message not found")
	}

	senderID := btc.Client.reactionSenderID(ctx, btc.Portal)
	if senderID == "" {
		return 0, errors.New("failed to resolve reaction sender")
	}

	receiver := btc.Portal.Receiver
	if receiver == "" {
		receiver = btc.Client.UserLogin.ID
	}

	reactions, err := btc.Client.UserLogin.Bridge.DB.Reaction.GetAllToMessageBySender(
		ctx,
		receiver,
		targetPart.ID,
		senderID,
	)
	if err != nil {
		return 0, err
	}

	normalizedEmoji := variationselector.Remove(emoji)
	var targets []*database.Reaction
	for _, reaction := range reactions {
		if reaction.MessagePartID != targetPart.PartID {
			continue
		}
		if normalizedEmoji != "" {
			reactionEmoji := reaction.Emoji
			if reactionEmoji == "" {
				reactionEmoji = string(reaction.EmojiID)
			}
			if reactionEmoji != normalizedEmoji {
				continue
			}
		}
		targets = append(targets, reaction)
	}

	removed := 0
	var firstErr error
	for _, reaction := range targets {
		_, err := intent.SendMessage(ctx, btc.Portal.MXID, event.EventRedaction, &event.Content{
			Parsed: &event.RedactionEventContent{
				Redacts: reaction.MXID,
			},
		}, &bridgev2.MatrixSendExtra{Timestamp: time.Now(), ReactionMeta: reaction})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := btc.Client.UserLogin.Bridge.DB.Reaction.Delete(ctx, reaction); err == nil {
			removed++
		}
	}

	if removed == 0 && firstErr != nil {
		return 0, firstErr
	}
	return removed, nil
}

// sendMatrixReadReceipt sends a read receipt for a message.
func sendMatrixReadReceipt(ctx context.Context, btc *BridgeToolContext, eventID id.EventID) error {
	if btc == nil || btc.Client == nil || btc.Client.UserLogin == nil || btc.Client.UserLogin.Bridge == nil || btc.Portal == nil {
		return nil
	}
	bot := btc.Client.UserLogin.Bridge.Bot
	if bot == nil {
		return nil
	}
	return bot.MarkRead(ctx, btc.Portal.MXID, eventID, time.Now())
}

// MatrixUserProfile represents a user's profile information.
type MatrixUserProfile struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
}

// getMatrixUserProfile gets a user's profile information.
func getMatrixUserProfile(ctx context.Context, btc *BridgeToolContext, userID id.UserID) (*MatrixUserProfile, error) {
	matrixConn := getMatrixConnector(btc)
	if matrixConn == nil || btc.Portal == nil {
		return nil, nil
	}

	profile, err := matrixConn.GetMemberInfo(ctx, btc.Portal.MXID, userID)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		return nil, nil
	}

	return &MatrixUserProfile{
		UserID:      userID.String(),
		DisplayName: profile.Displayname,
		AvatarURL:   string(profile.AvatarURL),
	}, nil
}

// MatrixRoomInfo represents room information.
type MatrixRoomInfo struct {
	RoomID      string `json:"room_id"`
	Name        string `json:"name,omitempty"`
	Topic       string `json:"topic,omitempty"`
	MemberCount int    `json:"member_count,omitempty"`
}

// getMatrixRoomInfo gets information about a room.
func getMatrixRoomInfo(ctx context.Context, btc *BridgeToolContext) (*MatrixRoomInfo, error) {
	matrixConn := getMatrixConnector(btc)
	if matrixConn == nil {
		return nil, nil
	}

	info := &MatrixRoomInfo{
		RoomID: btc.Portal.MXID.String(),
	}

	if stateConn, ok := matrixConn.(bridgev2.MatrixConnectorWithArbitraryRoomState); ok {
		// Get room name
		nameEvt, err := stateConn.GetStateEvent(ctx, btc.Portal.MXID, event.StateRoomName, "")
		if err == nil && nameEvt != nil {
			if content, ok := nameEvt.Content.Parsed.(*event.RoomNameEventContent); ok {
				info.Name = content.Name
			}
		}

		// Get room topic
		topicEvt, err := stateConn.GetStateEvent(ctx, btc.Portal.MXID, event.StateTopic, "")
		if err == nil && topicEvt != nil {
			if content, ok := topicEvt.Content.Parsed.(*event.TopicEventContent); ok {
				info.Topic = content.Topic
			}
		}
	}

	// Get member count using the connector
	members, err := matrixConn.GetMembers(ctx, btc.Portal.MXID)
	if err == nil {
		info.MemberCount = len(members)
	}

	return info, nil
}
