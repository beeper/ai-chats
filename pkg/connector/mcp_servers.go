package connector

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

const (
	mcpDefaultServerName       = "nexus"
	mcpServerKindGeneric       = "generic"
	mcpServerKindNexus         = "nexus"
	mcpTransportStreamableHTTP = "streamable_http"
	mcpTransportStdio          = "stdio"
)

type namedMCPServer struct {
	Name   string
	Config MCPServerConfig
	Source string // login|config
}

func normalizeMCPServerName(name string) string {
	trimmed := strings.TrimSpace(strings.ToLower(name))
	if trimmed == "" {
		return mcpDefaultServerName
	}
	return trimmed
}

func normalizeMCPServerKind(kind string) string {
	value := strings.TrimSpace(strings.ToLower(kind))
	if value == "" {
		return mcpServerKindGeneric
	}
	switch value {
	case "nexus", "clay":
		return mcpServerKindNexus
	case "generic":
		return mcpServerKindGeneric
	}
	return value
}

func normalizeMCPServerAuthType(authType string) string {
	value := strings.TrimSpace(strings.ToLower(authType))
	if value == "" {
		return "bearer"
	}
	switch value {
	case "bearer", "apikey", "api_key", "api-key", "none":
		return value
	default:
		return value
	}
}

func normalizeMCPServerTransport(transport string) string {
	value := strings.TrimSpace(strings.ToLower(transport))
	switch value {
	case "":
		return ""
	case mcpTransportStreamableHTTP, "streamable-http", "streamablehttp", "streamable", "http":
		return mcpTransportStreamableHTTP
	case mcpTransportStdio, "command", "cmd":
		return mcpTransportStdio
	default:
		return value
	}
}

func normalizeMCPCommandArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, 0, len(args))
	for _, raw := range args {
		part := strings.TrimSpace(raw)
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeMCPServerConfig(cfg MCPServerConfig) MCPServerConfig {
	cfg.Command = strings.TrimSpace(cfg.Command)
	cfg.Args = normalizeMCPCommandArgs(cfg.Args)
	if strings.TrimSpace(cfg.Transport) == "" {
		if cfg.Command != "" {
			cfg.Transport = mcpTransportStdio
		} else {
			cfg.Transport = mcpTransportStreamableHTTP
		}
	} else {
		cfg.Transport = normalizeMCPServerTransport(cfg.Transport)
	}

	cfg.Endpoint = strings.TrimSpace(cfg.Endpoint)
	if cfg.Transport != mcpTransportStdio {
		cfg.Endpoint = strings.TrimRight(cfg.Endpoint, "/")
	}
	cfg.Token = strings.TrimSpace(cfg.Token)
	cfg.AuthURL = strings.TrimSpace(cfg.AuthURL)
	cfg.Kind = normalizeMCPServerKind(cfg.Kind)
	cfg.AuthType = normalizeMCPServerAuthType(cfg.AuthType)
	if cfg.Transport == mcpTransportStdio {
		cfg.Endpoint = ""
		cfg.AuthType = "none"
	}
	return cfg
}

func mcpServerUsesStdio(cfg MCPServerConfig) bool {
	return normalizeMCPServerConfig(cfg).Transport == mcpTransportStdio
}

func mcpServerUsesHTTPTransport(cfg MCPServerConfig) bool {
	normalized := normalizeMCPServerConfig(cfg)
	return normalized.Transport == mcpTransportStreamableHTTP
}

func mcpServerHasTarget(cfg MCPServerConfig) bool {
	normalized := normalizeMCPServerConfig(cfg)
	if normalized.Transport == mcpTransportStdio {
		return normalized.Command != ""
	}
	return normalized.Endpoint != ""
}

func mcpServerNeedsToken(cfg MCPServerConfig) bool {
	normalized := normalizeMCPServerConfig(cfg)
	return mcpServerUsesHTTPTransport(normalized) && normalized.AuthType != "none"
}

func mcpServerTargetLabel(cfg MCPServerConfig) string {
	normalized := normalizeMCPServerConfig(cfg)
	if normalized.Transport == mcpTransportStdio {
		if len(normalized.Args) == 0 {
			return normalized.Command
		}
		return normalized.Command + " " + strings.Join(normalized.Args, " ")
	}
	return normalized.Endpoint
}

func (oc *AIClient) isMCPStdioEnabled() bool {
	if oc == nil || oc.connector == nil || oc.connector.Config.Tools.MCP == nil {
		return false
	}
	return oc.connector.Config.Tools.MCP.EnableStdio
}

func (oc *AIClient) isMCPTransportEnabled(cfg MCPServerConfig) bool {
	normalized := normalizeMCPServerConfig(cfg)
	switch normalized.Transport {
	case mcpTransportStdio:
		return oc.isMCPStdioEnabled()
	case mcpTransportStreamableHTTP:
		return true
	default:
		return false
	}
}

func (oc *AIClient) loginMCPServers() map[string]MCPServerConfig {
	if oc == nil || oc.UserLogin == nil {
		return nil
	}
	meta := loginMetadata(oc.UserLogin)
	if meta == nil || meta.ServiceTokens == nil || len(meta.ServiceTokens.MCPServers) == 0 {
		return nil
	}
	out := make(map[string]MCPServerConfig, len(meta.ServiceTokens.MCPServers))
	for rawName, rawCfg := range meta.ServiceTokens.MCPServers {
		name := normalizeMCPServerName(rawName)
		cfg := normalizeMCPServerConfig(rawCfg)
		if name == "" {
			continue
		}
		if !mcpServerHasTarget(cfg) {
			continue
		}
		if !oc.isMCPTransportEnabled(cfg) {
			continue
		}
		out[name] = cfg
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (oc *AIClient) configNexusMCPServer() (MCPServerConfig, bool) {
	if oc == nil || oc.connector == nil {
		return MCPServerConfig{}, false
	}
	cfg := oc.connector.Config.Tools.Nexus
	if !nexusConfigured(cfg) {
		return MCPServerConfig{}, false
	}
	endpoint := nexusMCPEndpoint(cfg)
	if endpoint == "" {
		return MCPServerConfig{}, false
	}
	return normalizeMCPServerConfig(MCPServerConfig{
		Transport: mcpTransportStreamableHTTP,
		Endpoint:  endpoint,
		AuthType:  cfg.AuthType,
		Token:     cfg.Token,
		Connected: true,
		Kind:      mcpServerKindNexus,
	}), true
}

func sortNamedMCPServers(servers []namedMCPServer) {
	slices.SortFunc(servers, func(a, b namedMCPServer) int {
		if a.Name == mcpDefaultServerName && b.Name != mcpDefaultServerName {
			return -1
		}
		if b.Name == mcpDefaultServerName && a.Name != mcpDefaultServerName {
			return 1
		}
		return cmp.Compare(a.Name, b.Name)
	})
}

func (oc *AIClient) configuredMCPServers() []namedMCPServer {
	loginServers := oc.loginMCPServers()
	servers := make([]namedMCPServer, 0, len(loginServers)+1)
	for name, cfg := range loginServers {
		servers = append(servers, namedMCPServer{Name: name, Config: cfg, Source: "login"})
	}
	if _, hasNexusOverride := loginServers[mcpDefaultServerName]; !hasNexusOverride {
		if cfg, ok := oc.configNexusMCPServer(); ok {
			servers = append(servers, namedMCPServer{Name: mcpDefaultServerName, Config: cfg, Source: "config"})
		}
	}
	sortNamedMCPServers(servers)
	return servers
}

func (oc *AIClient) configuredMCPServerByName(name string) (namedMCPServer, bool) {
	name = normalizeMCPServerName(name)
	for _, server := range oc.configuredMCPServers() {
		if server.Name == name {
			return server, true
		}
	}
	return namedMCPServer{}, false
}

func (oc *AIClient) activeMCPServers() []namedMCPServer {
	servers := oc.configuredMCPServers()
	active := make([]namedMCPServer, 0, len(servers))
	for _, server := range servers {
		cfg := normalizeMCPServerConfig(server.Config)
		if !cfg.Connected {
			continue
		}
		if !mcpServerHasTarget(cfg) {
			continue
		}
		if !oc.isMCPTransportEnabled(cfg) {
			continue
		}
		if mcpServerNeedsToken(cfg) && cfg.Token == "" {
			continue
		}
		server.Config = cfg
		active = append(active, server)
	}
	sortNamedMCPServers(active)
	return active
}

func (oc *AIClient) isMCPConfigured() bool {
	if oc == nil {
		return false
	}
	return len(oc.activeMCPServers()) > 0
}

func (oc *AIClient) hasConnectedClayMCP() bool {
	if oc == nil {
		return false
	}
	loginServers := oc.loginMCPServers()
	cfg, ok := loginServers[mcpDefaultServerName]
	if !ok {
		return false
	}
	cfg = normalizeMCPServerConfig(cfg)
	if normalizeMCPServerKind(cfg.Kind) != mcpServerKindNexus {
		return false
	}
	if !cfg.Connected || !mcpServerHasTarget(cfg) {
		return false
	}
	if mcpServerNeedsToken(cfg) && strings.TrimSpace(cfg.Token) == "" {
		return false
	}
	return true
}

func (oc *AIClient) activeNexusMCPServers() []namedMCPServer {
	servers := oc.activeMCPServers()
	active := make([]namedMCPServer, 0, len(servers))
	for _, server := range servers {
		if normalizeMCPServerKind(server.Config.Kind) == mcpServerKindNexus {
			active = append(active, server)
		}
	}
	sortNamedMCPServers(active)
	return active
}

func (oc *AIClient) invalidateNexusMCPToolCache() {
	if oc == nil {
		return
	}
	oc.nexusMCPToolsMu.Lock()
	oc.nexusMCPTools = nil
	oc.nexusMCPToolSet = nil
	oc.nexusMCPToolServer = nil
	oc.nexusMCPToolsFetchedAt = time.Time{}
	oc.nexusMCPToolsMu.Unlock()
}
