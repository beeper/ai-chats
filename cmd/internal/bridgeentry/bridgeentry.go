package bridgeentry

import (
	"maps"
	"slices"

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
	DummyBridge = Definition{
		Name:        "dummybridge",
		Description: "DummyBridge demo bridge built with the AgentRemote SDK.",
		Port:        29349,
		DBName:      "dummybridge.db",
	}
)

var registry = map[string]Definition{
	AI.Name:          AI,
	Codex.Name:       Codex,
	DummyBridge.Name: DummyBridge,
}

func Lookup(name string) (Definition, bool) {
	def, ok := registry[name]
	return def, ok
}

func Names() []string {
	return slices.Sorted(maps.Keys(registry))
}

func All() []Definition {
	names := Names()
	defs := make([]Definition, 0, len(names))
	for _, name := range names {
		defs = append(defs, registry[name])
	}
	return defs
}

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
	RunMain(m, tag, commit, buildTime)
}

func RunMain(m *mxmain.BridgeMain, tag, commit, buildTime string) {
	m.InitVersion(tag, commit, buildTime)
	m.Run()
}
