package main

import (
	"maunium.net/go/mautrix/bridgev2/matrix/mxmain"

	"github.com/beeper/ai-bridge/bridges/openclaw"
)

var (
	Tag       = "unknown"
	Commit    = "unknown"
	BuildTime = "unknown"
)

var m = mxmain.BridgeMain{
	Name:        "openclaw",
	Description: "A Matrix↔OpenClaw bridge built on mautrix-go bridgev2.",
	URL:         "https://github.com/beeper/ai-bridge",
	Version:     "0.1.0",
	Connector:   openclaw.NewConnector(),
}

func main() {
	m.InitVersion(Tag, Commit, BuildTime)
	m.Run()
}
