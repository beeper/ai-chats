package connector

import (
	"fmt"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
)

const queueDirectiveOptionsHint = "modes steer, followup, collect, steer+backlog, interrupt; debounce:<ms|s|m>, cap:<n>, drop:old|new|summarize"

func buildQueueStatusLine(settings airuntime.QueueSettings) string {
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
