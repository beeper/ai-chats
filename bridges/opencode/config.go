package opencode

import (
	_ "embed"

	"go.mau.fi/util/configupgrade"
)

const ProviderOpenCode = "opencode"

//go:embed example-config.yaml
var exampleNetworkConfig string

type Config struct {
	Bridge   BridgeConfig `yaml:"bridge"`
	OpenCode OpenCode     `yaml:"opencode"`
}

type BridgeConfig struct {
	CommandPrefix      string `yaml:"command_prefix"`
	StreamingTransport string `yaml:"streaming_transport"`        // ephemeral|debounced_edit
	StreamingDebounce  int    `yaml:"streaming_edit_debounce_ms"` // Debounce for edit transport
}

type OpenCode struct {
	Enabled *bool `yaml:"enabled"`
}

func upgradeConfig(helper configupgrade.Helper) {
	_ = helper
}
