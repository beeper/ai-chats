package connector

import (
	"context"

	"maunium.net/go/mautrix/event"
)

type statusEventsKey struct{}
type queueAcceptedStatusKey struct{}

func withStatusEvents(ctx context.Context, events []*event.Event) context.Context {
	if len(events) == 0 {
		return ctx
	}
	return context.WithValue(ctx, statusEventsKey{}, events)
}

func statusEventsFromContext(ctx context.Context) []*event.Event {
	return contextValue[[]*event.Event](ctx, statusEventsKey{})
}

func withQueueAcceptedStatus(ctx context.Context) context.Context {
	return context.WithValue(ctx, queueAcceptedStatusKey{}, true)
}

func queueAcceptedStatusFromContext(ctx context.Context) bool {
	return contextValue[bool](ctx, queueAcceptedStatusKey{})
}
