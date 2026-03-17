package main

import (
	"github.com/beeper/agentremote/bridges/dummybridge"
	"github.com/beeper/agentremote/cmd/internal/bridgeentry"
)

var (
	Tag       = "unknown"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func main() {
	bridgeentry.Run(bridgeentry.DummyBridge, dummybridge.NewConnector(), Tag, Commit, BuildTime)
}
