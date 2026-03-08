package openclaw

import (
	_ "embed"

	"go.mau.fi/util/configupgrade"

	"github.com/beeper/ai-bridge/pkg/shared/bridgeconfig"
)

const ProviderOpenClaw = "openclaw"

//go:embed example-config.yaml
var exampleNetworkConfig string

type Config struct {
	Bridge   bridgeconfig.BridgeConfig `yaml:"bridge"`
	OpenClaw OpenClawConfig            `yaml:"openclaw"`
}

type OpenClawConfig struct {
	Enabled *bool `yaml:"enabled"`
}

func upgradeConfig(_ configupgrade.Helper) {}
