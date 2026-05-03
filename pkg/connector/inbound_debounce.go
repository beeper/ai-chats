package connector

func (oc *AIClient) resolveInboundDebounceMs() int {
	if oc == nil || oc.connector == nil {
		return DefaultDebounceMs
	}
	cfg := oc.connector.Config
	if cfg.Messages != nil && cfg.Messages.InboundDebounce != nil {
		return max(cfg.Messages.InboundDebounce.DebounceMs, 0)
	}
	if cfg.Inbound != nil {
		return cfg.Inbound.WithDefaults().DefaultDebounceMs
	}
	return DefaultDebounceMs
}
