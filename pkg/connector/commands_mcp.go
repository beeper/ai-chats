package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2/commands"

	"github.com/beeper/ai-bridge/pkg/connector/commandregistry"
)

const mcpImplicitServerName = "default"

func mcpAddUsage(allowStdio bool) string {
	if allowStdio {
		return "`!ai mcp add <name> <endpoint> [token] [authType] [authURL]` | `!ai mcp add <name> streamable_http <endpoint> [token] [authType] [authURL]` | `!ai mcp add <name> stdio <command> [args...]` | `!ai mcp add <endpoint> [token] [authType] [authURL]`"
	}
	return "`!ai mcp add <name> <endpoint> [token] [authType] [authURL]` | `!ai mcp add <name> streamable_http <endpoint> [token] [authType] [authURL]` | `!ai mcp add <endpoint> [token] [authType] [authURL]`"
}

func mcpManageUsage(allowStdio bool) string {
	return fmt.Sprintf("`!ai mcp list` | %s | `!ai mcp connect [name] [token]` | `!ai mcp disconnect [name]` | `!ai mcp remove [name]`.", mcpAddUsage(allowStdio))
}

// CommandMCP handles the !ai mcp command.
var CommandMCP = registerAICommand(commandregistry.Definition{
	Name:          "mcp",
	Description:   "Manage MCP servers for this login",
	Args:          "<add|remove|connect|disconnect|list> [args]",
	Section:       HelpSectionAI,
	RequiresLogin: true,
	Handler:       fnMCPCommand,
})

func fnMCPCommand(ce *commands.Event) {
	client, ok := requireClient(ce)
	if !ok {
		return
	}
	allowStdio := client.isMCPStdioEnabled()

	if len(ce.Args) == 0 {
		ce.Reply("Usage: %s", mcpManageUsage(allowStdio))
		return
	}

	sub := strings.ToLower(strings.TrimSpace(ce.Args[0]))
	switch sub {
	case "list", "ls":
		fnMCPList(ce, client)
		return
	case "add":
		fnMCPAdd(ce, client)
		return
	case "connect":
		fnMCPConnect(ce, client)
		return
	case "disconnect":
		fnMCPDisconnect(ce, client)
		return
	case "remove", "rm", "delete":
		fnMCPRemove(ce, client)
		return
	default:
		ce.Reply("Usage: %s", mcpManageUsage(allowStdio))
	}
}

func fnMCPList(ce *commands.Event, client *AIClient) {
	servers := client.configuredMCPServers()
	if len(servers) == 0 {
		ce.Reply("MCP servers: none configured")
		return
	}

	toolCounts := map[string]int{}
	ctx, cancel := context.WithTimeout(ce.Ctx, 3*time.Second)
	defer cancel()
	defs, err := client.nexusMCPToolDefinitions(ctx)
	if err == nil {
		for _, def := range defs {
			name := client.cachedNexusMCPServerForTool(def.Name)
			if name == "" {
				continue
			}
			toolCounts[name]++
		}
	}

	lines := make([]string, 0, len(servers))
	for _, server := range servers {
		cfg := normalizeMCPServerConfig(server.Config)
		status := "disconnected"
		if cfg.Connected {
			status = "connected"
		}
		auth := cfg.AuthType
		if auth == "" {
			auth = "none"
		}
		token := "missing"
		if cfg.Token != "" || cfg.AuthType == "none" {
			token = "set"
		}
		line := fmt.Sprintf("- %s: %s (transport=%s, target=%s, auth=%s, token=%s)", server.Name, status, cfg.Transport, mcpServerTargetLabel(cfg), auth, token)
		if count, ok := toolCounts[server.Name]; ok {
			line = fmt.Sprintf("%s, tools=%d", line, count)
		}
		if server.Source == "config" {
			line += " [from config]"
		}
		lines = append(lines, line)
	}
	ce.Reply("MCP servers:\n%s", strings.Join(lines, "\n"))
}

func parseMCPHTTPAuthArgs(rest []string) (token string, authType string, authURL string) {
	token = ""
	authType = "bearer"
	authURL = ""
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

func isMCPTransportToken(value string) bool {
	switch normalizeMCPServerTransport(value) {
	case mcpTransportStreamableHTTP, mcpTransportStdio:
		return true
	default:
		return false
	}
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

	name = mcpImplicitServerName
	targetIndex := 0
	firstToken := strings.TrimSpace(trimmed[0])
	if !isLikelyHTTPURL(firstToken) && !isMCPTransportToken(firstToken) {
		if len(trimmed) < 2 {
			return "", MCPServerConfig{}, errors.New("missing target")
		}
		name = normalizeMCPServerName(firstToken)
		targetIndex = 1
	}

	if targetIndex >= len(trimmed) {
		return "", MCPServerConfig{}, errors.New("missing target")
	}

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

func fnMCPAdd(ce *commands.Event, client *AIClient) {
	login := client.UserLogin
	if login == nil {
		ce.Reply("No active login found")
		return
	}
	meta := loginMetadata(login)
	if meta == nil {
		ce.Reply("Failed to access login metadata")
		return
	}

	allowStdio := client.isMCPStdioEnabled()
	name, cfg, err := parseMCPAddArgs(ce.Args[1:], allowStdio)
	if err != nil {
		if err.Error() == "stdio disabled" {
			ce.Reply("Stdio MCP servers are disabled by bridge config.")
			return
		}
		ce.Reply("Usage: %s", mcpAddUsage(allowStdio))
		return
	}

	if meta.ServiceTokens == nil {
		meta.ServiceTokens = &ServiceTokens{}
	}
	if meta.ServiceTokens.MCPServers == nil {
		meta.ServiceTokens.MCPServers = map[string]MCPServerConfig{}
	}
	meta.ServiceTokens.MCPServers[name] = cfg
	if err := login.Save(ce.Ctx); err != nil {
		ce.Reply("Failed to save MCP server: %s", err)
		return
	}
	client.invalidateNexusMCPToolCache()

	ce.Reply("MCP server '%s' saved (%s). Run `!ai mcp connect %s` to connect.", name, mcpServerTargetLabel(cfg), name)
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
	if len(args) == 1 && len(servers) == 1 {
		// Allow `!ai mcp connect <token>` when only one server exists.
		return servers[0], strings.TrimSpace(args[0]), nil
	}
	return namedMCPServer{}, "", errors.New("not found")
}

func sendMCPAuthURLNotice(client *AIClient, ce *commands.Event, server namedMCPServer) {
	if strings.TrimSpace(server.Config.AuthURL) == "" {
		return
	}
	message := fmt.Sprintf("MCP authentication required for server '%s'. Open this URL: %s", server.Name, server.Config.AuthURL)
	if ce != nil && ce.Portal != nil {
		client.sendSystemNotice(ce.Ctx, ce.Portal, message)
		return
	}
	if ce != nil {
		ce.Reply(message)
	}
}

func (oc *AIClient) verifyMCPServerConnection(ctx context.Context, server namedMCPServer) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx := ctx
	var cancel context.CancelFunc
	if _, hasDeadline := callCtx.Deadline(); !hasDeadline {
		timeout := oc.nexusRequestTimeout()
		if timeout > 10*time.Second {
			timeout = 10 * time.Second
		}
		callCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	if cancel != nil {
		defer cancel()
	}
	defs, err := oc.fetchNexusMCPToolDefinitionsForServer(callCtx, server)
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

func fnMCPConnect(ce *commands.Event, client *AIClient) {
	login := client.UserLogin
	if login == nil {
		ce.Reply("No active login found")
		return
	}
	meta := loginMetadata(login)
	if meta == nil {
		ce.Reply("Failed to access login metadata")
		return
	}

	target, tokenOverride, err := resolveMCPServerArg(client, ce.Args[1:])
	if err != nil {
		switch err.Error() {
		case "none configured":
			ce.Reply("MCP servers: none configured. Use `!ai mcp add` first.")
		case "ambiguous":
			ce.Reply("Multiple MCP servers configured. Provide a server name. Use `!ai mcp list`.")
		default:
			ce.Reply("Unknown MCP server. Use `!ai mcp list`.")
		}
		return
	}

	cfg := normalizeMCPServerConfig(target.Config)
	if tokenOverride != "" && !mcpServerUsesStdio(cfg) {
		cfg.Token = strings.TrimSpace(tokenOverride)
		if cfg.Token != "" && cfg.AuthType == "none" {
			cfg.AuthType = "bearer"
		}
	}
	if !mcpServerHasTarget(cfg) {
		ce.Reply("MCP server '%s' has no target configured.", target.Name)
		return
	}
	if mcpServerNeedsToken(cfg) && cfg.Token == "" {
		cfg.Connected = false
		setLoginMCPServer(meta, target.Name, cfg)
		if saveErr := login.Save(ce.Ctx); saveErr != nil {
			ce.Reply("Failed to update MCP server '%s': %s", target.Name, saveErr)
			return
		}
		client.invalidateNexusMCPToolCache()
		sendMCPAuthURLNotice(client, ce, namedMCPServer{Name: target.Name, Config: cfg, Source: "login"})
		ce.Reply("MCP server '%s' is missing a token. Add one and run `!ai mcp connect %s <token>`.", target.Name, target.Name)
		return
	}

	cfg.Connected = true
	count, connectErr := client.verifyMCPServerConnection(ce.Ctx, namedMCPServer{Name: target.Name, Config: cfg, Source: "login"})
	if connectErr != nil {
		cfg.Connected = false
		setLoginMCPServer(meta, target.Name, cfg)
		if saveErr := login.Save(ce.Ctx); saveErr != nil {
			ce.Reply("Failed to save MCP server '%s': %s", target.Name, saveErr)
			return
		}
		client.invalidateNexusMCPToolCache()
		if mcpCallLikelyAuthError(connectErr) {
			sendMCPAuthURLNotice(client, ce, namedMCPServer{Name: target.Name, Config: cfg, Source: "login"})
		}
		ce.Reply("Failed to connect MCP server '%s': %v", target.Name, connectErr)
		return
	}

	setLoginMCPServer(meta, target.Name, cfg)
	if err := login.Save(ce.Ctx); err != nil {
		ce.Reply("Failed to save MCP server '%s': %s", target.Name, err)
		return
	}
	client.invalidateNexusMCPToolCache()
	ce.Reply("Connected MCP server '%s' (%d tools discovered).", target.Name, count)
}

func fnMCPDisconnect(ce *commands.Event, client *AIClient) {
	login := client.UserLogin
	if login == nil {
		ce.Reply("No active login found")
		return
	}
	meta := loginMetadata(login)
	if meta == nil {
		ce.Reply("Failed to access login metadata")
		return
	}

	target, _, err := resolveMCPServerArg(client, ce.Args[1:])
	if err != nil {
		switch err.Error() {
		case "none configured":
			ce.Reply("MCP servers: none configured")
		case "ambiguous":
			ce.Reply("Multiple MCP servers configured. Provide a server name. Use `!ai mcp list`.")
		default:
			ce.Reply("Unknown MCP server. Use `!ai mcp list`.")
		}
		return
	}

	cfg := normalizeMCPServerConfig(target.Config)
	cfg.Connected = false
	setLoginMCPServer(meta, target.Name, cfg)
	if err := login.Save(ce.Ctx); err != nil {
		ce.Reply("Failed to disconnect MCP server '%s': %s", target.Name, err)
		return
	}
	client.invalidateNexusMCPToolCache()
	ce.Reply("Disconnected MCP server '%s'.", target.Name)
}

func fnMCPRemove(ce *commands.Event, client *AIClient) {
	login := client.UserLogin
	if login == nil {
		ce.Reply("No active login found")
		return
	}
	meta := loginMetadata(login)
	if meta == nil {
		ce.Reply("Failed to access login metadata")
		return
	}

	target, _, err := resolveMCPServerArg(client, ce.Args[1:])
	if err != nil {
		switch err.Error() {
		case "none configured":
			ce.Reply("MCP servers: none configured")
		case "ambiguous":
			ce.Reply("Multiple MCP servers configured. Provide a server name. Use `!ai mcp list`.")
		default:
			ce.Reply("Unknown MCP server. Use `!ai mcp list`.")
		}
		return
	}

	loginServers := client.loginMCPServers()
	if _, ok := loginServers[target.Name]; !ok {
		ce.Reply("MCP server '%s' comes from bridge config and cannot be removed from login metadata. Use `!ai mcp disconnect %s` to override it for this login.", target.Name, target.Name)
		return
	}

	clearLoginMCPServer(meta, target.Name)
	if err := login.Save(ce.Ctx); err != nil {
		ce.Reply("Failed to remove MCP server '%s': %s", target.Name, err)
		return
	}
	client.invalidateNexusMCPToolCache()
	ce.Reply("Removed MCP server '%s'.", target.Name)
}
