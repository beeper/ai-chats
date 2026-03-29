package ai

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

func isLikelyHTTPURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
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
	creds := ensureLoginCredentials(meta)
	if creds == nil {
		return
	}
	if creds.ServiceTokens == nil {
		creds.ServiceTokens = &ServiceTokens{}
	}
	if creds.ServiceTokens.MCPServers == nil {
		creds.ServiceTokens.MCPServers = map[string]MCPServerConfig{}
	}
	creds.ServiceTokens.MCPServers[name] = normalizeMCPServerConfig(cfg)
}

func clearLoginMCPServer(meta *UserLoginMetadata, name string) {
	creds := loginCredentials(meta)
	if creds == nil || creds.ServiceTokens == nil || creds.ServiceTokens.MCPServers == nil {
		return
	}
	delete(creds.ServiceTokens.MCPServers, name)
	if len(creds.ServiceTokens.MCPServers) == 0 {
		creds.ServiceTokens.MCPServers = nil
	}
	if serviceTokensEmpty(creds.ServiceTokens) {
		creds.ServiceTokens = nil
	}
	if loginCredentialsEmpty(creds) {
		meta.Credentials = nil
	}
}
