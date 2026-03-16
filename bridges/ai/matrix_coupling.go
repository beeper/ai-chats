package ai

import (
	"context"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// These helpers isolate the remaining Matrix-connector-specific hooks we still need
// until bridgev2 exposes connector-agnostic delayed-event and custom-event APIs.

type schedulerDelayedEventIntent interface {
	SendMessageEvent(ctx context.Context, roomID id.RoomID, eventType event.Type, content any, extra ...mautrix.ReqSendEvent) (*mautrix.RespSendEvent, error)
	DelayedEvents(ctx context.Context, req *mautrix.ReqDelayedEvents) (*mautrix.RespDelayedEvents, error)
	UpdateDelayedEvent(ctx context.Context, req *mautrix.ReqUpdateDelayedEvent) (*mautrix.RespUpdateDelayedEvent, error)
}

func resolveSchedulerDelayedEventIntent(login *bridgev2.UserLogin) schedulerDelayedEventIntent {
	if login == nil || login.Bridge == nil {
		return nil
	}
	bot, ok := login.Bridge.Bot.(*matrix.ASIntent)
	if !ok || bot == nil {
		return nil
	}
	return bot.Matrix
}

func registerScheduleTickEventHandler(br *bridgev2.Bridge, handler func(context.Context, *event.Event)) bool {
	if br == nil {
		return false
	}
	matrixConnector, ok := br.Matrix.(*matrix.Connector)
	if !ok || matrixConnector == nil {
		return false
	}
	matrixConnector.EventProcessor.On(ScheduleTickEventType, handler)
	return true
}
