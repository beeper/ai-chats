package main

import (
	aibridge "github.com/beeper/ai-chats/bridges/ai"
	"maunium.net/go/mautrix/bridgev2/matrix/mxmain"
)

// Information to find out exactly which commit the bridge was built from.
// These are filled at build time with the -X linker flag.
var (
	Tag       = "unknown"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func main() {
	m := mxmain.BridgeMain{
		Name:        "ai",
		Description: "AI bridge for Beeper.",
		URL:         "https://github.com/beeper/ai-chats",
		Version:     "0.1.0",
		Connector:   aibridge.NewAIConnector(),
	}
	m.InitVersion(Tag, Commit, BuildTime)
	m.Run()
}
