package sdk

import (
	"context"
	"maps"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/matrixevents"
)

func (t *Turn) buildPlaceholderMessage() *bridgev2.ConvertedMessage {
	extra := map[string]any{}
	msgContent := &event.MessageEventContent{
		MsgType:  event.MsgText,
		Body:     "...",
		Mentions: &event.Mentions{},
	}
	var dbMetadata any
	if t.placeholderPayload != nil {
		if t.placeholderPayload.Content != nil {
			cloned := *t.placeholderPayload.Content
			msgContent = &cloned
		}
		if len(t.placeholderPayload.Extra) > 0 {
			extra = maps.Clone(t.placeholderPayload.Extra)
			if extra == nil {
				extra = map[string]any{}
			}
		}
		dbMetadata = t.placeholderPayload.DBMetadata
	}
	if msgContent.Mentions == nil {
		msgContent.Mentions = &event.Mentions{}
	}
	if _, ok := extra[matrixevents.BeeperAIKey]; !ok {
		extra[matrixevents.BeeperAIKey] = map[string]any{
			"id":   t.turnID,
			"role": "assistant",
			"metadata": map[string]any{
				"turn_id": t.turnID,
			},
			"parts": []any{},
		}
	}
	if t.session != nil {
		if descriptor, err := t.session.Descriptor(t.turnCtx); err == nil && descriptor != nil {
			msgContent.BeeperStream = descriptor
		}
	}
	if relatesTo := t.buildRelatesTo(); relatesTo != nil {
		msgContent.RelatesTo = relatesTo
	}
	return &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:         networkid.PartID("0"),
			Type:       event.EventMessage,
			Content:    msgContent,
			Extra:      extra,
			DBMetadata: dbMetadata,
		}},
	}
}
func (t *Turn) buildRelatesTo() *event.RelatesTo {
	if t.threadRoot != "" {
		replyTo := t.replyTo
		if replyTo == "" && t.source != nil && t.source.EventID != "" {
			replyTo = id.EventID(t.source.EventID)
		}
		return (&event.RelatesTo{}).SetThread(t.threadRoot, replyTo)
	}
	if t.replyTo != "" {
		return (&event.RelatesTo{}).SetReplyTo(t.replyTo)
	}
	return nil
}
func (t *Turn) ensureStarted() {
	t.mu.Lock()
	if t.started || t.ended {
		t.mu.Unlock()
		return
	}
	t.started = true
	t.mu.Unlock()
	if t.conv != nil {
		if agent := t.resolveAgent(t.turnCtx); agent != nil {
			t.agent = agent
			if err := t.conv.EnsureRoomAgent(t.turnCtx, agent); err != nil && t.startErr == nil {
				t.startErr = err
			}
		}
	}
	t.ensureSession()
	if !t.SuppressSend() {
		if t.sendFunc != nil {
			evtID, msgID, err := t.sendFunc(t.turnCtx)
			if err == nil {
				t.applyPlaceholderSendResult(evtID, msgID)
			} else if t.startErr == nil {
				t.startErr = err
			}
		} else if t.conv != nil && t.conv.portal != nil && t.conv.login != nil {
			identity := t.providerIdentity()
			timing := ResolveEventTiming(time.UnixMilli(t.startedAtMs), 0)
			sender := t.resolveSender(t.turnCtx)
			if err := t.ensureSenderJoined(t.turnCtx, sender, bridgev2.RemoteEventMessage); err != nil && t.startErr == nil {
				t.startErr = err
			}
			evtID, msgID, err := SendViaPortal(SendViaPortalParams{
				Login:       t.conv.login,
				Portal:      t.conv.portal,
				Sender:      sender,
				IDPrefix:    identity.IDPrefix,
				LogKey:      identity.LogKey,
				Timestamp:   timing.Timestamp,
				StreamOrder: timing.StreamOrder,
				Converted:   t.buildPlaceholderMessage(),
			})
			if err == nil {
				t.applyPlaceholderSendResult(evtID, msgID)
			} else if t.startErr == nil {
				t.startErr = err
			}
		}
	}
	baseMeta := map[string]any{
		"turnId": t.turnID,
	}
	if t.agent != nil {
		baseMeta["agentId"] = t.agent.ID
		if t.agent.ModelKey != "" {
			baseMeta["modelKey"] = t.agent.ModelKey
		}
	}
	t.Writer().Start(t.turnCtx, baseMeta)
}
func (t *Turn) ensureSenderJoined(ctx context.Context, sender bridgev2.EventSender, eventType bridgev2.RemoteEventType) error {
	if t == nil || t.conv == nil || t.conv.login == nil || t.conv.login.Bridge == nil || t.conv.portal == nil || t.conv.portal.Bridge == nil || t.conv.portal.MXID == "" || sender.Sender == "" {
		return nil
	}
	intent, ok := t.conv.portal.GetIntentFor(ctx, sender, t.conv.login, eventType)
	if !ok || intent == nil {
		return nil
	}
	return intent.EnsureJoined(ctx, t.conv.portal.MXID)
}
func (t *Turn) applyPlaceholderSendResult(evtID id.EventID, msgID networkid.MessageID) {
	t.mu.Lock()
	t.initialEventID = evtID
	t.networkMessageID = msgID
	t.mu.Unlock()
	if evtID != "" && t.session != nil {
		if streamErr := t.session.Start(t.turnCtx, evtID); streamErr != nil && t.startErr == nil {
			t.startErr = streamErr
		}
	}
	t.ensureStreamStartedAsync()
}
