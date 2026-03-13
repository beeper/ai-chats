package main

import (
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/matrix/mxmain"

	aibridge "github.com/beeper/agentremote/bridges/ai"
	"github.com/beeper/agentremote/bridges/codex"
	"github.com/beeper/agentremote/bridges/openclaw"
	"github.com/beeper/agentremote/bridges/opencode"
)

type bridgeDef struct {
	Name        string
	Description string
	NewFunc     func() bridgev2.NetworkConnector
	Port        int
	DBName      string
}

var bridgeRegistry = map[string]bridgeDef{
	"ai": {
		Name:        "ai",
		Description: "A Matrix↔AI bridge for Beeper built on mautrix-go bridgev2.",
		NewFunc:     func() bridgev2.NetworkConnector { return aibridge.NewAIConnector() },
		Port:        29345,
		DBName:      "ai.db",
	},
	"codex": {
		Name:        "codex",
		Description: "A Matrix↔Codex bridge built on mautrix-go bridgev2.",
		NewFunc:     func() bridgev2.NetworkConnector { return codex.NewConnector() },
		Port:        29346,
		DBName:      "codex.db",
	},
	"opencode": {
		Name:        "opencode",
		Description: "A Matrix↔OpenCode bridge built on mautrix-go bridgev2.",
		NewFunc:     func() bridgev2.NetworkConnector { return opencode.NewConnector() },
		Port:        29347,
		DBName:      "opencode.db",
	},
	"openclaw": {
		Name:        "openclaw",
		Description: "A Matrix↔OpenClaw bridge built on mautrix-go bridgev2.",
		NewFunc:     func() bridgev2.NetworkConnector { return openclaw.NewConnector() },
		Port:        29348,
		DBName:      "openclaw.db",
	},
}

func newBridgeMain(def bridgeDef) *mxmain.BridgeMain {
	return &mxmain.BridgeMain{
		Name:        def.Name,
		Description: def.Description,
		URL:         "https://github.com/beeper/agentremote",
		Version:     "0.1.0",
		Connector:   def.NewFunc(),
	}
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
