package main

import (
	"strings"

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
	"dummybridge": {
		Definition: bridgeentry.DummyBridge,
		NewFunc:    func() bridgev2.NetworkConnector { return dummybridge.NewConnector() },
	},
}

func beeperBridgeName(deviceID, bridgeType, name string) string {
	base := "sh-" + strings.TrimSpace(deviceID) + "-" + bridgeType
	if name == "" {
		return base
	}
	return base + "-" + name
}

func instanceDirName(bridgeType, name string) string {
	if name == "" {
		return bridgeType
	}
	return bridgeType + "-" + name
}

func splitInstanceName(instanceName string) (bridgeType, name string, ok bool) {
	instanceName = strings.TrimSpace(instanceName)
	longest := ""
	for candidate := range bridgeRegistry {
		if instanceName == candidate || strings.HasPrefix(instanceName, candidate+"-") {
			if len(candidate) > len(longest) {
				longest = candidate
			}
		}
	}
	if longest == "" {
		return "", "", false
	}
	if instanceName == longest {
		return longest, "", true
	}
	return longest, strings.TrimPrefix(instanceName, longest+"-"), true
}

func remoteBridgeNameForLocalInstance(deviceID, instanceName string) (string, bool) {
	bridgeType, name, ok := splitInstanceName(instanceName)
	if !ok {
		return "", false
	}
	return beeperBridgeName(deviceID, bridgeType, name), true
}

func localInstanceNameForRemoteBridge(deviceID, remoteName string) (string, bool) {
	prefix := "sh-" + strings.TrimSpace(deviceID) + "-"
	suffix, ok := strings.CutPrefix(remoteName, prefix)
	if !ok {
		return "", false
	}
	if _, _, ok := splitInstanceName(suffix); !ok {
		return "", false
	}
	return suffix, true
}
