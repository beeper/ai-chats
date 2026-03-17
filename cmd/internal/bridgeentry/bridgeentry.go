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
		Description: "AgentRemote bridge entry for Beeper built on mautrix-go bridgev2.",
		Port:        29345,
		DBName:      "ai.db",
	}
	Codex = Definition{
		Name:        "codex",
		Description: "A Matrix↔Codex bridge built on mautrix-go bridgev2.",
		Port:        29346,
		DBName:      "codex.db",
	}
	OpenCode = Definition{
		Name:        "opencode",
		Description: "A Matrix↔OpenCode bridge built on mautrix-go bridgev2.",
		Port:        29347,
		DBName:      "opencode.db",
	}
	OpenClaw = Definition{
		Name:        "openclaw",
		Description: "A Matrix↔OpenClaw bridge built on mautrix-go bridgev2.",
		Port:        29348,
		DBName:      "openclaw.db",
	}
	DummyBridge = Definition{
		Name:        "dummybridge",
		Description: "A Matrix↔DummyBridge demo bridge built on the AgentRemote SDK.",
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
