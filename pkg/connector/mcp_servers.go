package connector

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

const (
	mcpDefaultServerName       = "default"
	mcpServerKindGeneric       = "generic"
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
	return value
}

func normalizeMCPServerAuthType(authType string) string {
	value := strings.TrimSpace(strings.ToLower(authType))
	if value == "" {
		return "bearer"
	}
	return value
}

func normalizeMCPServerTransport(transport string) string {
	return strings.TrimSpace(strings.ToLower(transport))
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

func mcpServerHasTarget(cfg MCPServerConfig) bool {
	normalized := normalizeMCPServerConfig(cfg)
	if normalized.Transport == mcpTransportStdio {
		return normalized.Command != ""
	}
	return normalized.Endpoint != ""
}

func mcpServerNeedsToken(cfg MCPServerConfig) bool {
	normalized := normalizeMCPServerConfig(cfg)
	return normalized.Transport == mcpTransportStreamableHTTP && normalized.AuthType != "none"
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

func sortNamedMCPServers(servers []namedMCPServer) {
	slices.SortFunc(servers, func(a, b namedMCPServer) int { return cmp.Compare(a.Name, b.Name) })
}

func (oc *AIClient) configuredMCPServers() []namedMCPServer {
	loginServers := oc.loginMCPServers()
	servers := make([]namedMCPServer, 0, len(loginServers))
	for name, cfg := range loginServers {
		servers = append(servers, namedMCPServer{Name: name, Config: cfg, Source: "login"})
	}
	sortNamedMCPServers(servers)
	return servers
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

func (oc *AIClient) invalidateMCPToolCache() {
	if oc == nil {
		return
	}
	oc.mcpToolsMu.Lock()
	oc.mcpTools = nil
	oc.mcpToolSet = nil
	oc.mcpToolServer = nil
	oc.mcpToolsFetchedAt = time.Time{}
	oc.mcpToolsMu.Unlock()
}
