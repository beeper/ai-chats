package openclaw

import (
	_ "embed"

	"go.mau.fi/util/configupgrade"

	"github.com/beeper/agentremote/pkg/shared/bridgeconfig"
)

const ProviderOpenClaw = "openclaw"

//go:embed example-config.yaml
var exampleNetworkConfig string

type Config struct {
	Bridge   bridgeconfig.BridgeConfig `yaml:"bridge"`
	OpenClaw OpenClawConfig            `yaml:"openclaw"`
}

type OpenClawConfig struct {
	Enabled   *bool                   `yaml:"enabled"`
	Discovery OpenClawDiscoveryConfig `yaml:"discovery"`
}

type OpenClawDiscoveryConfig struct {
	Enabled           *bool  `yaml:"enabled"`
	TimeoutMS         int    `yaml:"timeout_ms"`
	WideAreaDomain    string `yaml:"wide_area_domain"`
	PrefillTTLSeconds int    `yaml:"prefill_ttl_seconds"`
}

func upgradeConfig(_ configupgrade.Helper) {}
