package main

import (
	"github.com/beeper/agentremote/bridges/opencode"
	"github.com/beeper/agentremote/cmd/internal/bridgeentry"
)

var (
	Tag       = "unknown"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func main() {
	bridgeentry.Run(bridgeentry.OpenCode, opencode.NewConnector(), Tag, Commit, BuildTime)
}
