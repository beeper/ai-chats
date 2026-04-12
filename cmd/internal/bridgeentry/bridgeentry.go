package bridgeentry

import (
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/matrix/mxmain"
)

const (
	RepoURL = "https://github.com/beeper/agentremote"
	Version = "0.1.0"
)

type Definition struct {
	Name        string
	Description string
	Port        int
	DBName      string
}

var (
	AI = Definition{
		Name:        "ai",
		Description: "AI bridge built with the AgentRemote SDK.",
		Port:        29345,
		DBName:      "ai.db",
	}
	Codex = Definition{
		Name:        "codex",
		Description: "Codex bridge built with the AgentRemote SDK.",
		Port:        29346,
		DBName:      "codex.db",
	}
	OpenCode = Definition{
		Name:        "opencode",
		Description: "OpenCode bridge built with the AgentRemote SDK.",
		Port:        29347,
		DBName:      "opencode.db",
	}
	OpenClaw = Definition{
		Name:        "openclaw",
		Description: "OpenClaw Gateway bridge built with the AgentRemote SDK.",
		Port:        29348,
		DBName:      "openclaw.db",
	}
	DummyBridge = Definition{
		Name:        "dummybridge",
		Description: "DummyBridge demo bridge built with the AgentRemote SDK.",
		Port:        29349,
		DBName:      "dummybridge.db",
	}
)

func (d Definition) NewMain(connector bridgev2.NetworkConnector) *mxmain.BridgeMain {
	return &mxmain.BridgeMain{
		Name:        d.Name,
		Description: d.Description,
		URL:         RepoURL,
		Version:     Version,
		Connector:   connector,
	}
}

func Run(def Definition, connector bridgev2.NetworkConnector, tag, commit, buildTime string) {
	m := def.NewMain(connector)
	m.InitVersion(tag, commit, buildTime)
	m.Run()
}
