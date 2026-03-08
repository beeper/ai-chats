package connector

type ResolvedHeartbeatVisibility struct {
	ShowOk       bool
	ShowAlerts   bool
	UseIndicator bool
}

var defaultHeartbeatVisibility = ResolvedHeartbeatVisibility{
	ShowOk:       false,
	ShowAlerts:   true,
	UseIndicator: true,
}

func resolveHeartbeatVisibility(cfg *Config, channel string) ResolvedHeartbeatVisibility {
	if cfg == nil || cfg.Channels == nil {
		return defaultHeartbeatVisibility
	}

	defaults := cfg.Channels.Defaults
	perChannel := cfg.Channels.Matrix
	if channel != "" && channel != "matrix" {
		perChannel = nil
	}

	result := ResolvedHeartbeatVisibility{
		ShowOk:       defaultHeartbeatVisibility.ShowOk,
		ShowAlerts:   defaultHeartbeatVisibility.ShowAlerts,
		UseIndicator: defaultHeartbeatVisibility.UseIndicator,
	}

	if defaults != nil {
		applyHeartbeatVisibility(&result, defaults.Heartbeat)
	}
	if perChannel != nil {
		applyHeartbeatVisibility(&result, perChannel.Heartbeat)
	}

	return result
}

func applyHeartbeatVisibility(dst *ResolvedHeartbeatVisibility, cfg *ChannelHeartbeatVisibilityConfig) {
	if dst == nil || cfg == nil {
		return
	}
	if cfg.ShowOk != nil {
		dst.ShowOk = *cfg.ShowOk
	}
	if cfg.ShowAlerts != nil {
		dst.ShowAlerts = *cfg.ShowAlerts
	}
	if cfg.UseIndicator != nil {
		dst.UseIndicator = *cfg.UseIndicator
	}
}
