package ai

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
)

type acceptedMessageEcho struct {
	simplevent.EventMeta

	messageID     networkid.MessageID
	transactionID networkid.TransactionID
}

var (
	_ bridgev2.RemoteMessage                  = (*acceptedMessageEcho)(nil)
	_ bridgev2.RemoteMessageWithTransactionID = (*acceptedMessageEcho)(nil)
)

func (evt *acceptedMessageEcho) ConvertMessage(context.Context, *bridgev2.Portal, bridgev2.MatrixAPI) (*bridgev2.ConvertedMessage, error) {
	return nil, bridgev2.ErrIgnoringRemoteEvent
}

func (evt *acceptedMessageEcho) GetID() networkid.MessageID {
	return evt.messageID
}

func (evt *acceptedMessageEcho) GetTransactionID() networkid.TransactionID {
	return evt.transactionID
}

func acceptedUserMessageTxnID(msg *bridgev2.MatrixMessage) networkid.TransactionID {
	if msg == nil {
		return ""
	}
	if msg.InputTransactionID != "" {
		return networkid.TransactionID(msg.InputTransactionID)
	}
	if msg.Event != nil && msg.Event.ID != "" {
		return networkid.TransactionID("mxevt:" + msg.Event.ID.String())
	}
	return ""
}

func (oc *AIClient) registerPendingUserMessage(
	msg *bridgev2.MatrixMessage,
	portal *bridgev2.Portal,
	userMessage *database.Message,
) {
	if oc == nil || msg == nil || portal == nil || userMessage == nil {
		return
	}
	txnID := acceptedUserMessageTxnID(msg)
	if txnID == "" {
		return
	}
	userMessage.SendTxnID = networkid.RawTransactionID(txnID)
	msg.AddPendingToSave(userMessage, txnID, func(_ bridgev2.RemoteMessage, echoed *database.Message) (bool, error) {
		oc.persistAcceptedUserMessage(oc.backgroundContext(context.Background()), portal, echoed)
		return false, nil
	})
}

func (oc *AIClient) acceptPendingMessages(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	if oc == nil || portal == nil || portal.MXID == "" || state == nil || state.suppressSend {
		return
	}

	messages := oc.consumeRoomRunAcceptedMessages(state.roomID)
	if len(messages) == 0 {
		return
	}

	for _, msg := range messages {
		if msg == nil {
			continue
		}
		txnID := networkid.TransactionID(msg.SendTxnID)
		if txnID == "" {
			oc.loggerForContext(ctx).Warn().
				Str("message_id", string(msg.ID)).
				Str("event_id", msg.MXID.String()).
				Msg("Skipping accepted message echo without transaction ID")
			continue
		}
		ts := msg.Timestamp
		if ts.IsZero() {
			ts = time.Now()
		}
		result := oc.UserLogin.QueueRemoteEvent(&acceptedMessageEcho{
			EventMeta: simplevent.EventMeta{
				Type:        bridgev2.RemoteEventMessage,
				PortalKey:   portal.PortalKey,
				Sender:      bridgev2.EventSender{Sender: msg.SenderID, SenderLogin: oc.UserLogin.ID},
				Timestamp:   ts,
				StreamOrder: ts.UnixMilli(),
			},
			messageID:     msg.ID,
			transactionID: txnID,
		})
		if !result.Success {
			err := result.Error
			if err == nil {
				err = fmt.Errorf("queue remote event failed")
			}
			oc.loggerForContext(ctx).Warn().
				Err(err).
				Str("message_id", string(msg.ID)).
				Str("txn_id", string(txnID)).
				Msg("Failed to queue accepted message echo")
		}
	}
}
