package codex

import (
	"time"

	"go.mau.fi/util/configupgrade"

	"github.com/beeper/ai-bridge/bridges/codex/codexrpc"
	"github.com/beeper/ai-bridge/pkg/shared/bridgeconfig"
)

const ProviderCodex = "codex"

type Config struct {
	Bridge             bridgeconfig.BridgeConfig `yaml:"bridge"`
	Codex              *CodexConfig              `yaml:"codex"`
	Owners             []string                  `yaml:"owners"`
	ModelCacheDuration time.Duration             `yaml:"model_cache_duration"`
}

// CodexConfig configures the Codex app-server integration.
type CodexConfig struct {
	Enabled       *bool            `yaml:"enabled"`
	Command       string           `yaml:"command"`
	Listen        string           `yaml:"listen"`
	HomeBaseDir   string           `yaml:"home_base_dir"`
	DefaultModel  string           `yaml:"default_model"`
	NetworkAccess *bool            `yaml:"network_access"`
	ClientInfo    *CodexClientInfo `yaml:"client_info"`
}

type CodexClientInfo struct {
	Name    string `yaml:"name"`
	Title   string `yaml:"title"`
	Version string `yaml:"version"`
}

func (ci *CodexClientInfo) rpcClientInfo() codexrpc.ClientInfo {
	return codexrpc.ClientInfo{Name: ci.Name, Title: ci.Title, Version: ci.Version}
}

const exampleNetworkConfig = `
bridge:
  command_prefix: "!ai"
  streaming_transport: "ephemeral"
  streaming_edit_debounce_ms: 250
codex:
  enabled: true
  command: "codex"
  listen: "stdio://"
  default_model: "gpt-5.1-codex"
  network_access: true
  client_info:
    name: "ai_bridge_matrix"
    title: "AI Bridge (Matrix)"
    version: "0.1.0"
`

func upgradeConfig(_ configupgrade.Helper) {}
