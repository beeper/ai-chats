package tools

import "github.com/beeper/agentremote/pkg/shared/toolspec"

// BeeperSendFeedbackTool is the Beeper feedback submission tool.
var BeeperSendFeedbackTool = newUnavailableTool(
	toolspec.BeeperSendFeedbackName,
	toolspec.BeeperSendFeedbackDescription,
	"Beeper Send Feedback",
	toolspec.BeeperSendFeedbackSchema(),
	GroupWeb,
	toolspec.BeeperSendFeedbackName+" is only available through the connector",
)
