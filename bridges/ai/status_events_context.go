package ai

import (
	"context"

	"maunium.net/go/mautrix/event"
)

type statusEventsKey struct{}

func statusEventsFromContext(ctx context.Context) []*event.Event {
	return contextValue[[]*event.Event](ctx, statusEventsKey{})
}
