package bridgeconfig

// BridgeConfig tweaks Matrix-side behaviour shared across all AI bridges.
type BridgeConfig struct {
	CommandPrefix      string `yaml:"command_prefix"`
	LogEphemeralEvents *bool  `yaml:"log_ephemeral_events,omitempty"`
	StreamingTransport string `yaml:"streaming_transport"`        // ephemeral|debounced_edit
	StreamingDebounce  int    `yaml:"streaming_edit_debounce_ms"` // Debounce for edit transport
}
