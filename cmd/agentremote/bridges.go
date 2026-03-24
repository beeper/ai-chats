package main

import (
	"maunium.net/go/mautrix/bridgev2"

	aibridge "github.com/beeper/agentremote/bridges/ai"
	"github.com/beeper/agentremote/bridges/codex"
	"github.com/beeper/agentremote/bridges/dummybridge"
	"github.com/beeper/agentremote/bridges/openclaw"
	"github.com/beeper/agentremote/bridges/opencode"
	"github.com/beeper/agentremote/cmd/internal/bridgeentry"
)

type bridgeDef struct {
	bridgeentry.Definition
	NewFunc           func() bridgev2.NetworkConnector
	RuntimeBridgeType string
	RemoteBridgeType  string
	ConfigOverrides   map[string]any
}

var bridgeRegistry = map[string]bridgeDef{
	"ai": {
		Definition:        bridgeentry.AI,
		NewFunc:           func() bridgev2.NetworkConnector { return aibridge.NewAIConnector() },
		RuntimeBridgeType: "ai",
		RemoteBridgeType:  "ai",
		ConfigOverrides: map[string]any{
			"network.agents.enabled": false,
		},
	},
	"agent": {
		Definition:        bridgeentry.Agent,
		NewFunc:           func() bridgev2.NetworkConnector { return aibridge.NewAIConnector() },
		RuntimeBridgeType: "ai",
		RemoteBridgeType:  "ai",
		ConfigOverrides: map[string]any{
			"network.agents.enabled": true,
		},
	},
	"codex": {
		Definition:       bridgeentry.Codex,
		NewFunc:          func() bridgev2.NetworkConnector { return codex.NewConnector() },
		RemoteBridgeType: "codex",
	},
	"opencode": {
		Definition:       bridgeentry.OpenCode,
		NewFunc:          func() bridgev2.NetworkConnector { return opencode.NewConnector() },
		RemoteBridgeType: "opencode",
	},
	"openclaw": {
		Definition:       bridgeentry.OpenClaw,
		NewFunc:          func() bridgev2.NetworkConnector { return openclaw.NewConnector() },
		RemoteBridgeType: "openclaw",
	},
	"dummybridge": {
		Definition:       bridgeentry.DummyBridge,
		NewFunc:          func() bridgev2.NetworkConnector { return dummybridge.NewConnector() },
		RemoteBridgeType: "dummybridge",
	},
}

func remoteBridgeType(localBridgeType string) string {
	def, ok := bridgeRegistry[localBridgeType]
	if !ok || def.RemoteBridgeType == "" {
		return localBridgeType
	}
	return def.RemoteBridgeType
}

func runtimeBridgeType(localBridgeType string) string {
	def, ok := bridgeRegistry[localBridgeType]
	if !ok || def.RuntimeBridgeType == "" {
		return localBridgeType
	}
	return def.RuntimeBridgeType
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
