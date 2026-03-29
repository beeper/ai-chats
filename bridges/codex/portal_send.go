package codex

import "maunium.net/go/mautrix/bridgev2"

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
