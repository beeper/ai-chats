package connector

func traceLevel(meta *PortalMetadata) string {
	_ = meta
	return "off"
}

func traceEnabled(meta *PortalMetadata) bool {
	_ = meta
	return false
}

func traceFull(meta *PortalMetadata) bool {
	_ = meta
	return false
}
