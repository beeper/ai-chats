package connector

import airuntime "github.com/beeper/ai-bridge/pkg/runtime"

// isAbortTrigger returns true when the message body is a bare "panic button" token.
// This intentionally remains independent of the command system: users can type e.g.
// "stop" to abort an in-flight run.
func isAbortTrigger(text string) bool {
	return airuntime.IsAbortTriggerText(text)
}
