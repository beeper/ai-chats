package ai

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func (oc *AIClient) persistAIConversationMessage(ctx context.Context, portal *bridgev2.Portal, msg *database.Message) error {
	return oc.persistAIConversationMessages(ctx, portal, []*database.Message{msg})
}

func (oc *AIClient) persistAIConversationMessages(ctx context.Context, portal *bridgev2.Portal, messages []*database.Message) error {
	return withResolvedPortalScope(ctx, oc, portal, func(ctx context.Context, portal *bridgev2.Portal, scope *portalScope) error {
		return scope.db.DoTxn(ctx, nil, func(ctx context.Context) error {
			for _, msg := range messages {
				if msg == nil {
					continue
				}
				meta, ok := msg.Metadata.(*MessageMetadata)
				if !ok || meta == nil {
					continue
				}
				turnData, ok := canonicalTurnData(meta)
				if !ok {
					continue
				}
				if err := upsertAITurnByScope(ctx, scope, portal, aiTurnUpsert{
					TurnID:           strings.TrimSpace(turnData.ID),
					Kind:             aiTurnKindConversation,
					MessageID:        msg.ID,
					EventID:          msg.MXID,
					SenderID:         msg.SenderID,
					IncludeInHistory: !meta.ExcludeFromHistory,
					Timestamp:        msg.Timestamp,
					TurnData:         turnData,
					Metadata:         meta,
				}); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

func loadAIConversationMessageByScope(
	ctx context.Context,
	scope *portalScope,
	portal *bridgev2.Portal,
	messageID networkid.MessageID,
	eventID id.EventID,
) (*database.Message, error) {
	record, err := loadAITurnByRefByScope(ctx, scope, messageID, eventID)
	if err != nil || record == nil {
		return nil, err
	}
	if record.Kind != aiTurnKindConversation {
		return nil, nil
	}
	return databaseMessageFromAITurn(portal, record), nil
}

func (oc *AIClient) loadAIConversationMessage(
	ctx context.Context,
	portal *bridgev2.Portal,
	messageID networkid.MessageID,
	eventID id.EventID,
) (*database.Message, error) {
	return withResolvedPortalScopeValue(ctx, oc, portal, func(ctx context.Context, portal *bridgev2.Portal, scope *portalScope) (*database.Message, error) {
		return loadAIConversationMessageByScope(ctx, scope, portal, messageID, eventID)
	})
}

func databaseMessageFromAITurn(portal *bridgev2.Portal, record *aiTurnRecord) *database.Message {
	if record == nil {
		return nil
	}
	msg := &database.Message{
		ID:        record.MessageID,
		MXID:      record.EventID,
		SenderID:  record.SenderID,
		Timestamp: time.UnixMilli(record.CreatedAtMs),
		Metadata:  normalizeAITurnMetadata(record.Metadata, record.TurnData),
	}
	if msg.ID == "" {
		msg.ID = networkid.MessageID(record.TurnID)
	}
	if portal != nil {
		msg.Room = portal.PortalKey
	}
	return msg
}

func aiHistoryMessageFromTurn(portalKey networkid.PortalKey, row *aiTurnRecord) *database.Message {
	if row == nil {
		return nil
	}
	msgID := row.MessageID
	if msgID == "" {
		msgID = networkid.MessageID(row.TurnID)
	}
	timestampMs := row.CreatedAtMs
	if timestampMs == 0 {
		timestampMs = row.UpdatedAtMs
	}
	msg := &database.Message{
		ID:       msgID,
		MXID:     row.EventID,
		Room:     portalKey,
		PartID:   networkid.PartID("0"),
		SenderID: row.SenderID,
		Metadata: cloneMessageMetadata(row.Metadata),
	}
	if timestampMs > 0 {
		msg.Timestamp = time.UnixMilli(timestampMs)
	}
	return msg
}
