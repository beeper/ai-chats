package main

import (
	"github.com/beeper/agentremote/bridges/openclaw"
	"github.com/beeper/agentremote/cmd/internal/bridgeentry"
)

var (
	Tag       = "unknown"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func main() {
	bridgeentry.Run(bridgeentry.OpenClaw, openclaw.NewConnector(), Tag, Commit, BuildTime)
}
