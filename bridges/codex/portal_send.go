package codex

import (
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func (cc *CodexClient) sendViaPortal(
	portal *bridgev2.Portal,
	converted *bridgev2.ConvertedMessage,
	msgID networkid.MessageID,
	timestamp time.Time,
	streamOrder int64,
) (id.EventID, networkid.MessageID, error) {
	return cc.ClientBase.SendViaPortalWithOptions(portal, cc.senderForPortal(), msgID, timestamp, streamOrder, converted)
}

func (cc *CodexClient) senderForPortal() bridgev2.EventSender {
	if cc == nil || cc.UserLogin == nil {
		return bridgev2.EventSender{Sender: codexGhostID}
	}
	return bridgev2.EventSender{Sender: codexGhostID, SenderLogin: cc.UserLogin.ID}
}

func (cc *CodexClient) senderForHuman() bridgev2.EventSender {
	if cc == nil || cc.UserLogin == nil {
		return bridgev2.EventSender{IsFromMe: true}
	}
	return bridgev2.EventSender{Sender: cc.HumanUserID(), SenderLogin: cc.UserLogin.ID, IsFromMe: true}
}
