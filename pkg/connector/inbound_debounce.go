package connector

func normalizeDebounceMs(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func (oc *AIClient) resolveInboundDebounceMs(channel string) int {
	if oc == nil || oc.connector == nil {
		return DefaultDebounceMs
	}
	cfg := oc.connector.Config
	if cfg.Messages != nil && cfg.Messages.InboundDebounce != nil {
		if byChannel := cfg.Messages.InboundDebounce.ByChannel; byChannel != nil {
			if v, ok := byChannel[channel]; ok {
				return normalizeDebounceMs(v)
			}
		}
		return normalizeDebounceMs(cfg.Messages.InboundDebounce.DebounceMs)
	}
	if cfg.Inbound != nil {
		return cfg.Inbound.WithDefaults().DefaultDebounceMs
	}
	return DefaultDebounceMs
}
