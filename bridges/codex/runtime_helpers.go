package codex

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

func messageSendStatusError(err error, message string, reason event.MessageStatusReason) error {
	return bridgeadapter.MessageSendStatusError(err, message, reason, messageStatusForError, messageStatusReasonForError)
}

func newBrokenLoginClient(login *bridgev2.UserLogin, connector *CodexConnector, reason string) *bridgeadapter.BrokenLoginClient {
	c := bridgeadapter.NewBrokenLoginClient(login, reason)
	c.OnLogout = func(ctx context.Context, login *bridgev2.UserLogin) {
		tmp := &CodexClient{UserLogin: login, connector: connector}
		tmp.purgeCodexHomeBestEffort(ctx)
		tmp.purgeCodexCwdsBestEffort(ctx)
		if connector != nil && login != nil {
			bridgeadapter.RemoveClientFromCache(&connector.clientsMu, connector.clients, login.ID)
		}
	}
	return c
}
