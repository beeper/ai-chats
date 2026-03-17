package dummybridge

import (
	_ "embed"

	"go.mau.fi/util/configupgrade"

	"github.com/beeper/agentremote/pkg/shared/bridgeconfig"
)

const ProviderDummyBridge = "dummybridge"

//go:embed example-config.yaml
var exampleNetworkConfig string

type Config struct {
	Bridge      bridgeconfig.BridgeConfig `yaml:"bridge"`
	DummyBridge DummyBridgeConfig         `yaml:"dummybridge"`
}

type DummyBridgeConfig struct {
	Enabled *bool `yaml:"enabled"`
}

func upgradeConfig(_ configupgrade.Helper) {}
