package main

import (
	"github.com/beeper/agentremote/bridges/codex"
	"github.com/beeper/agentremote/cmd/internal/bridgeentry"
)

var (
	Tag       = "unknown"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func main() {
	bridgeentry.Run(bridgeentry.Codex, codex.NewConnector(), Tag, Commit, BuildTime)
}
