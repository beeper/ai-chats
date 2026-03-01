package connector

import (
	"context"

	"maunium.net/go/mautrix/event"
)

type statusEventsKey struct{}
type queueAcceptedStatusKey struct{}

func statusEventsFromContext(ctx context.Context) []*event.Event {
	return contextValue[[]*event.Event](ctx, statusEventsKey{})
}

func queueAcceptedStatusFromContext(ctx context.Context) bool {
	return contextValue[bool](ctx, queueAcceptedStatusKey{})
}
