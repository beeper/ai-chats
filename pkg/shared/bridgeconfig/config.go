package bridgeconfig

// BridgeConfig tweaks Matrix-side behaviour shared across all AI bridges.
type BridgeConfig struct {
	CommandPrefix string `yaml:"command_prefix"`
}
