package opencode

import (
	_ "embed"

	"go.mau.fi/util/configupgrade"

	"github.com/beeper/ai-bridge/pkg/shared/bridgeconfig"
)

const ProviderOpenCode = "opencode"

//go:embed example-config.yaml
var exampleNetworkConfig string

type Config struct {
	Bridge   bridgeconfig.BridgeConfig `yaml:"bridge"`
	OpenCode OpenCode                  `yaml:"opencode"`
}

type OpenCode struct {
	Enabled *bool `yaml:"enabled"`
}

func upgradeConfig(helper configupgrade.Helper) {
	_ = helper
}
