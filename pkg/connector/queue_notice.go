package connector

import (
	"fmt"
	"strings"
)

const queueDirectiveOptionsHint = "modes steer, followup, collect, steer+backlog, interrupt; debounce:<ms|s|m>, cap:<n>, drop:old|new|summarize"

func buildQueueDirectiveAck(directive QueueDirective) string {
	var parts []string
	if directive.QueueMode != "" {
		parts = append(parts, formatSystemAck(fmt.Sprintf("Queue mode set to %s.", directive.QueueMode)))
	} else if directive.QueueReset {
		parts = append(parts, formatSystemAck("Queue mode reset to default."))
	}
	if directive.DebounceMs != nil {
		parts = append(parts, formatSystemAck(fmt.Sprintf("Queue debounce set to %dms.", *directive.DebounceMs)))
	}
	if directive.Cap != nil {
		parts = append(parts, formatSystemAck(fmt.Sprintf("Queue cap set to %d.", *directive.Cap)))
	}
	if directive.DropPolicy != nil {
		parts = append(parts, formatSystemAck(fmt.Sprintf("Queue drop set to %s.", *directive.DropPolicy)))
	}
	return strings.Join(parts, " ")
}

func buildQueueStatusLine(settings QueueSettings) string {
	debounceLabel := fmt.Sprintf("%dms", settings.DebounceMs)
	capLabel := fmt.Sprintf("%d", settings.Cap)
	dropLabel := string(settings.DropPolicy)
	return fmt.Sprintf(
		"Current queue settings: mode=%s, debounce=%s, cap=%s, drop=%s.\nOptions: %s.",
		settings.Mode,
		debounceLabel,
		capLabel,
		dropLabel,
		queueDirectiveOptionsHint,
	)
}
