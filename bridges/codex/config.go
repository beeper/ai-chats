package codex

import (
	"time"

	"go.mau.fi/util/configupgrade"

	"github.com/beeper/agentremote/pkg/shared/bridgeconfig"
)

const ProviderCodex = "codex"

const (
	defaultCodexClientInfoName  = "codex_bridge_matrix"
	defaultCodexClientInfoTitle = "Codex Bridge (Matrix)"
)

type Config struct {
	Bridge             bridgeconfig.BridgeConfig `yaml:"bridge"`
	Codex              *CodexConfig              `yaml:"codex"`
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

const exampleNetworkConfig = `
bridge:
  command_prefix: "!ai"
codex:
  enabled: true
  command: "codex"
  listen: ""
  default_model: "gpt-5.1-codex"
  network_access: true
  client_info:
    name: "codex_bridge_matrix"
    title: "Codex Bridge (Matrix)"
    version: "0.1.0"
`

func upgradeConfig(_ configupgrade.Helper) {}
