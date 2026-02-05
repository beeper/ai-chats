package opencodebridge

import (
	"net/url"
	"strings"
)

func (b *Bridge) opencodeInstanceConfig(instanceID string) *OpenCodeInstance {
	if b == nil || b.host == nil {
		return nil
	}
	meta := b.host.OpenCodeInstances()
	if meta == nil {
		return nil
	}
	return meta[instanceID]
}

func (b *Bridge) opencodeDisplayName(instanceID string) string {
	cfg := b.opencodeInstanceConfig(instanceID)
	return opencodeLabelFromURL(cfg)
}

func opencodeLabelFromURL(cfg *OpenCodeInstance) string {
	label := "OpenCode"
	if cfg == nil {
		return label
	}
	raw := strings.TrimSpace(cfg.URL)
	if raw == "" {
		return label
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return label
	}
	host := strings.TrimSpace(parsed.Host)
	if host == "" {
		host = strings.TrimSpace(parsed.Path)
	}
	if host == "" {
		return label
	}
	return label + " (" + host + ")"
}
