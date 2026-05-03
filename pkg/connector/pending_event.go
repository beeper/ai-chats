package connector

import "maunium.net/go/mautrix/event"

// snapshotPendingEvent copies only the event fields that queued/goroutine-based
// reply targeting and status propagation depend on.
func snapshotPendingEvent(evt *event.Event) *event.Event {
	if evt == nil {
		return nil
	}
	cloned := &event.Event{
		ID:     evt.ID,
		Type:   evt.Type,
		Sender: evt.Sender,
	}
	if len(evt.Content.Raw) > 0 {
		cloned.Content.Raw = clonePendingRawMap(evt.Content.Raw)
	}
	return cloned
}

func clonePendingRawMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(src))
	for k, v := range src {
		cloned[k] = clonePendingRawValue(v)
	}
	return cloned
}

func clonePendingRawSlice(src []any) []any {
	if len(src) == 0 {
		return nil
	}
	cloned := make([]any, len(src))
	for i, v := range src {
		cloned[i] = clonePendingRawValue(v)
	}
	return cloned
}

func clonePendingRawValue(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		return clonePendingRawMap(typed)
	case []any:
		return clonePendingRawSlice(typed)
	case []byte:
		return append([]byte(nil), typed...)
	default:
		return v
	}
}
