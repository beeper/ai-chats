package main

import (
	"maunium.net/go/mautrix/bridgev2"

	aibridge "github.com/beeper/agentremote/bridges/ai"
	"github.com/beeper/agentremote/bridges/codex"
	"github.com/beeper/agentremote/bridges/openclaw"
	"github.com/beeper/agentremote/bridges/opencode"
	"github.com/beeper/agentremote/cmd/internal/bridgeentry"
)

type bridgeDef struct {
	bridgeentry.Definition
	NewFunc func() bridgev2.NetworkConnector
}

var bridgeRegistry = map[string]bridgeDef{
	"ai": {
		Definition: bridgeentry.AI,
		NewFunc:    func() bridgev2.NetworkConnector { return aibridge.NewAIConnector() },
	},
	"codex": {
		Definition: bridgeentry.Codex,
		NewFunc:    func() bridgev2.NetworkConnector { return codex.NewConnector() },
	},
	"opencode": {
		Definition: bridgeentry.OpenCode,
		NewFunc:    func() bridgev2.NetworkConnector { return opencode.NewConnector() },
	},
	"openclaw": {
		Definition: bridgeentry.OpenClaw,
		NewFunc:    func() bridgev2.NetworkConnector { return openclaw.NewConnector() },
	},
}

func beeperBridgeName(bridgeType, name string) string {
	if name == "" {
		return "sh-" + bridgeType
	}
	return "sh-" + bridgeType + "-" + name
}

func instanceDirName(bridgeType, name string) string {
	if name == "" {
		return bridgeType
	}
	return bridgeType + "-" + name
}
