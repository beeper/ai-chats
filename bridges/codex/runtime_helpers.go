package codex

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote"
)

const AIAuthFailed status.BridgeStateErrorCode = "ai-auth-failed"

func messageStatusForError(_ error) event.MessageStatus {
	return event.MessageStatusRetriable
}

func messageStatusReasonForError(_ error) event.MessageStatusReason {
	return event.MessageStatusGenericError
}

func messageSendStatusError(err error, message string, reason event.MessageStatusReason) error {
	return agentremote.MessageSendStatusError(err, message, reason, messageStatusForError, messageStatusReasonForError)
}

func newBrokenLoginClient(login *bridgev2.UserLogin, connector *CodexConnector, reason string) *agentremote.BrokenLoginClient {
	c := agentremote.NewBrokenLoginClient(login, reason)
	c.OnLogout = func(ctx context.Context, login *bridgev2.UserLogin) {
		tmp := &CodexClient{UserLogin: login, connector: connector}
		tmp.purgeCodexHomeBestEffort(ctx)
		tmp.purgeCodexCwdsBestEffort(ctx)
		if connector != nil && login != nil {
			agentremote.RemoveClientFromCache(&connector.clientsMu, connector.clients, login.ID)
		}
	}
	return c
}
