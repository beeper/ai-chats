package agentremote

import (
	"time"

	"github.com/beeper/agentremote/pkg/shared/backfillutil"
)

// EventTiming carries the explicit timestamp and stream order for a live event.
type EventTiming struct {
	Timestamp   time.Time
	StreamOrder int64
}

// ResolveEventTiming fills in missing live-event timing metadata using the
// shared backfill stream-order semantics.
func ResolveEventTiming(timestamp time.Time, streamOrder int64) EventTiming {
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	if streamOrder == 0 {
		streamOrder = backfillutil.NextStreamOrder(0, timestamp)
	}
	return EventTiming{
		Timestamp:   timestamp,
		StreamOrder: streamOrder,
	}
}

// NextEventTiming allocates the next strictly increasing stream order for a
// sequence of related live events.
func NextEventTiming(lastStreamOrder int64, timestamp time.Time) EventTiming {
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	return EventTiming{
		Timestamp:   timestamp,
		StreamOrder: backfillutil.NextStreamOrder(lastStreamOrder, timestamp),
	}
}
