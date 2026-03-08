package tools

import "github.com/beeper/ai-bridge/pkg/shared/toolspec"

// BeeperSendFeedbackTool is the Beeper feedback submission tool.
var BeeperSendFeedbackTool = newConnectorOnlyTool(
	toolspec.BeeperSendFeedbackName,
	toolspec.BeeperSendFeedbackDescription,
	"Beeper Send Feedback",
	toolspec.BeeperSendFeedbackSchema(),
)
