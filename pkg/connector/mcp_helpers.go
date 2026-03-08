package connector

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

func mcpAddUsage(allowStdio bool) string {
	if allowStdio {
		return "`!ai mcp add <name> <endpoint> [token] [authType] [authURL]` | `!ai mcp add <name> streamable_http <endpoint> [token] [authType] [authURL]` | `!ai mcp add <name> stdio <command> [args...]`"
	}
	return "`!ai mcp add <name> <endpoint> [token] [authType] [authURL]` | `!ai mcp add <name> streamable_http <endpoint> [token] [authType] [authURL]`"
}

func mcpManageUsage(allowStdio bool) string {
	return fmt.Sprintf("`!ai mcp list` | %s | `!ai mcp connect [name] [token]` | `!ai mcp disconnect [name]` | `!ai mcp remove [name]`.", mcpAddUsage(allowStdio))
}

func isLikelyHTTPURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func parseMCPHTTPAuthArgs(rest []string) (token, authType, authURL string) {
	authType = "bearer"
	if len(rest) > 0 {
		token = strings.TrimSpace(rest[0])
	}
	if len(rest) > 1 {
		authType = strings.TrimSpace(rest[1])
	}
	if len(rest) > 2 {
		authURL = strings.TrimSpace(strings.Join(rest[2:], " "))
	}
	return token, authType, authURL
}

func parseMCPAddArgs(args []string, allowStdio bool) (name string, cfg MCPServerConfig, err error) {
	trimmed := make([]string, 0, len(args))
	for _, raw := range args {
		part := strings.TrimSpace(raw)
		if part != "" {
			trimmed = append(trimmed, part)
		}
	}
	if len(trimmed) == 0 {
		return "", MCPServerConfig{}, errors.New("missing args")
	}

	if len(trimmed) < 2 {
		return "", MCPServerConfig{}, errors.New("missing target")
	}
	name = normalizeMCPServerName(trimmed[0])
	targetIndex := 1

	rawTransportOrTarget := strings.TrimSpace(trimmed[targetIndex])
	normalizedTransport := normalizeMCPServerTransport(rawTransportOrTarget)
	if normalizedTransport == mcpTransportStdio {
		if !allowStdio {
			return "", MCPServerConfig{}, errors.New("stdio disabled")
		}
		if len(trimmed) <= targetIndex+1 {
			return "", MCPServerConfig{}, errors.New("missing command")
		}
		cfg = normalizeMCPServerConfig(MCPServerConfig{
			Transport: mcpTransportStdio,
			Command:   strings.TrimSpace(trimmed[targetIndex+1]),
			Args:      trimmed[targetIndex+2:],
			AuthType:  "none",
			Connected: false,
			Kind:      mcpServerKindGeneric,
		})
		if cfg.Command == "" {
			return "", MCPServerConfig{}, errors.New("missing command")
		}
		return name, cfg, nil
	}

	endpoint := rawTransportOrTarget
	rest := trimmed[targetIndex+1:]
	if normalizedTransport == mcpTransportStreamableHTTP {
		if len(trimmed) <= targetIndex+1 {
			return "", MCPServerConfig{}, errors.New("missing endpoint")
		}
		endpoint = strings.TrimSpace(trimmed[targetIndex+1])
		rest = trimmed[targetIndex+2:]
	}
	if !isLikelyHTTPURL(endpoint) {
		return "", MCPServerConfig{}, errors.New("invalid endpoint")
	}
	token, authType, authURL := parseMCPHTTPAuthArgs(rest)
	cfg = normalizeMCPServerConfig(MCPServerConfig{
		Transport: mcpTransportStreamableHTTP,
		Endpoint:  endpoint,
		Token:     token,
		AuthType:  authType,
		AuthURL:   authURL,
		Connected: false,
		Kind:      mcpServerKindGeneric,
	})
	return name, cfg, nil
}

func resolveMCPServerArg(client *AIClient, args []string) (namedMCPServer, string, error) {
	servers := client.configuredMCPServers()
	if len(servers) == 0 {
		return namedMCPServer{}, "", errors.New("none configured")
	}

	if len(args) == 0 {
		if len(servers) == 1 {
			return servers[0], "", nil
		}
		return namedMCPServer{}, "", errors.New("ambiguous")
	}

	candidate := strings.TrimSpace(args[0])
	for _, server := range servers {
		if server.Name == normalizeMCPServerName(candidate) {
			token := ""
			if len(args) > 1 {
				token = strings.TrimSpace(strings.Join(args[1:], " "))
			}
			return server, token, nil
		}
	}
	return namedMCPServer{}, "", errors.New("not found")
}

func (oc *AIClient) verifyMCPServerConnection(ctx context.Context, server namedMCPServer) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx := ctx
	var cancel context.CancelFunc
	if _, hasDeadline := callCtx.Deadline(); !hasDeadline {
		timeout := oc.mcpRequestTimeout()
		if timeout > 10*time.Second {
			timeout = 10 * time.Second
		}
		callCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	if cancel != nil {
		defer cancel()
	}
	defs, err := oc.fetchMCPToolsForServer(callCtx, server)
	if err != nil {
		return 0, err
	}
	return len(defs), nil
}

func setLoginMCPServer(meta *UserLoginMetadata, name string, cfg MCPServerConfig) {
	if meta.ServiceTokens == nil {
		meta.ServiceTokens = &ServiceTokens{}
	}
	if meta.ServiceTokens.MCPServers == nil {
		meta.ServiceTokens.MCPServers = map[string]MCPServerConfig{}
	}
	meta.ServiceTokens.MCPServers[name] = normalizeMCPServerConfig(cfg)
}

func clearLoginMCPServer(meta *UserLoginMetadata, name string) {
	if meta == nil || meta.ServiceTokens == nil || meta.ServiceTokens.MCPServers == nil {
		return
	}
	delete(meta.ServiceTokens.MCPServers, name)
	if len(meta.ServiceTokens.MCPServers) == 0 {
		meta.ServiceTokens.MCPServers = nil
	}
	if serviceTokensEmpty(meta.ServiceTokens) {
		meta.ServiceTokens = nil
	}
}
