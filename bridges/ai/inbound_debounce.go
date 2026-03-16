package ai

func (oc *AIClient) resolveInboundDebounceMs(channel string) int {
	if oc == nil || oc.connector == nil {
		return DefaultDebounceMs
	}
	cfg := oc.connector.Config
	if cfg.Messages != nil && cfg.Messages.InboundDebounce != nil {
		if byChannel := cfg.Messages.InboundDebounce.ByChannel; byChannel != nil {
			if v, ok := byChannel[channel]; ok {
				return max(v, 0)
			}
		}
		return max(cfg.Messages.InboundDebounce.DebounceMs, 0)
	}
	if cfg.Inbound != nil {
		return cfg.Inbound.WithDefaults().DefaultDebounceMs
	}
	return DefaultDebounceMs
}
