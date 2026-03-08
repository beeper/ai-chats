package connector

func resolveResponsePrefixForHeartbeat(oc *AIClient, cfg *Config, agentID string, meta *PortalMetadata) string {
	_ = oc
	_ = cfg
	_ = agentID
	_ = meta
	return ""
}

func resolveResponsePrefixForReply(oc *AIClient, cfg *Config, meta *PortalMetadata) string {
	_ = oc
	_ = cfg
	_ = meta
	return ""
}
