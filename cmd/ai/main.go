package main

import (
	"maunium.net/go/mautrix/bridgev2"

	aibridge "github.com/beeper/agentremote/bridges/ai"
	"github.com/beeper/agentremote/cmd/internal/bridgeentry"
)

// Information to find out exactly which commit the bridge was built from.
// These are filled at build time with the -X linker flag.
var (
	Tag       = "unknown"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func main() {
	bridgev2.PortalEventBuffer = 0
	bridgeentry.Run(bridgeentry.AI, aibridge.NewAIConnector(), Tag, Commit, BuildTime)
}
