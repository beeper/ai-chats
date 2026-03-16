package tools

import "github.com/beeper/agentremote/pkg/shared/toolspec"

// BeeperDocsTool is the Beeper help documentation search tool.
var BeeperDocsTool = newUnavailableTool(
	toolspec.BeeperDocsName,
	toolspec.BeeperDocsDescription,
	"Beeper Docs",
	toolspec.BeeperDocsSchema(),
	GroupWeb,
	toolspec.BeeperDocsName+" is only available through the connector",
)
