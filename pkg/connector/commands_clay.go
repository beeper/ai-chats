package connector

import (
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2/commands"

	"github.com/beeper/ai-bridge/pkg/connector/commandregistry"
)

const (
	defaultClayMCPEndpoint = "https://nexum.clay.earth/mcp"
	clayManageUsage        = "`!ai clay` | `!ai clay <token>` | `!ai clay connect [token]` | `!ai clay status` | `!ai clay disconnect` | `!ai clay remove`."
)

// CommandClay provides a shortcut for Clay/Nexus MCP bootstrap.
var CommandClay = registerAICommand(commandregistry.Definition{
	Name:          "clay",
	Description:   "Quick setup for Clay MCP (Nexus)",
	Args:          "[connect|status|disconnect|remove|token]",
	Section:       HelpSectionAI,
	RequiresLogin: true,
	Handler:       fnClayCommand,
})

func fnClayCommand(ce *commands.Event) {
	client, ok := requireClient(ce)
	if !ok {
		return
	}

	if len(ce.Args) == 0 {
		fnClayConnect(ce, client, "")
		return
	}

	sub := strings.ToLower(strings.TrimSpace(ce.Args[0]))
	switch sub {
	case "connect", "start":
		token := strings.TrimSpace(strings.Join(ce.Args[1:], " "))
		fnClayConnect(ce, client, token)
		return
	case "status", "list", "ls":
		fnClayStatus(ce, client)
		return
	case "disconnect", "stop":
		fnClayDisconnect(ce, client)
		return
	case "remove", "rm", "delete":
		fnClayRemove(ce, client)
		return
	default:
		// Treat args as token for one-command setup flow.
		token := strings.TrimSpace(strings.Join(ce.Args, " "))
		fnClayConnect(ce, client, token)
	}
}

func clayEndpointForClient(client *AIClient) string {
	if client == nil {
		return defaultClayMCPEndpoint
	}
	if server, ok := client.configuredMCPServerByName(mcpDefaultServerName); ok {
		endpoint := strings.TrimSpace(server.Config.Endpoint)
		if endpoint != "" {
			return endpoint
		}
	}
	if client.connector != nil && client.connector.Config.Tools.Nexus != nil {
		endpoint := strings.TrimSpace(mcpEndpointFromNexusConfig(client.connector.Config.Tools.Nexus))
		if endpoint != "" {
			return endpoint
		}
	}
	return defaultClayMCPEndpoint
}

func clayMCPServerConfig(client *AIClient, tokenOverride string) MCPServerConfig {
	cfg := MCPServerConfig{
		Transport: mcpTransportStreamableHTTP,
		Endpoint:  clayEndpointForClient(client),
		AuthType:  "bearer",
		Connected: false,
		Kind:      mcpServerKindNexus,
	}
	if existing, ok := client.configuredMCPServerByName(mcpDefaultServerName); ok {
		cfg = existing.Config
	}
	if normalizeMCPServerTransport(cfg.Transport) != mcpTransportStreamableHTTP {
		cfg.Transport = mcpTransportStreamableHTTP
		cfg.Command = ""
		cfg.Args = nil
	}
	if normalizeMCPServerKind(cfg.Kind) == mcpServerKindGeneric {
		cfg.Kind = mcpServerKindNexus
	}
	if strings.TrimSpace(cfg.Endpoint) == "" {
		cfg.Endpoint = clayEndpointForClient(client)
	}
	if strings.TrimSpace(cfg.AuthType) == "" {
		cfg.AuthType = "bearer"
	}
	if token := strings.TrimSpace(tokenOverride); token != "" {
		cfg.Token = token
	}
	return normalizeMCPServerConfig(cfg)
}

func fnClayStatus(ce *commands.Event, client *AIClient) {
	server, ok := client.configuredMCPServerByName(mcpDefaultServerName)
	if !ok {
		ce.Reply("Clay MCP is not configured. Run `!ai clay <token>` to bootstrap. Usage: %s", clayManageUsage)
		return
	}
	cfg := normalizeMCPServerConfig(server.Config)
	status := "disconnected"
	if cfg.Connected {
		status = "connected"
	}
	token := "missing"
	if cfg.Token != "" || cfg.AuthType == "none" {
		token = "set"
	}
	kind := normalizeMCPServerKind(cfg.Kind)
	msg := fmt.Sprintf("Clay MCP: %s (transport=%s, target=%s, kind=%s, auth=%s, token=%s)", status, cfg.Transport, mcpServerTargetLabel(cfg), kind, cfg.AuthType, token)
	if server.Source == "config" {
		msg += " [from config]"
	}
	if cfg.AuthURL != "" {
		msg += fmt.Sprintf("\nAuth URL: %s", cfg.AuthURL)
	}
	ce.Reply(msg)
}

func fnClayConnect(ce *commands.Event, client *AIClient, tokenOverride string) {
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

	cfg := clayMCPServerConfig(client, tokenOverride)
	if !mcpServerHasTarget(cfg) {
		ce.Reply("Clay MCP target is empty. Usage: %s", clayManageUsage)
		return
	}
	if mcpServerNeedsToken(cfg) && cfg.Token == "" {
		cfg.Connected = false
		setLoginMCPServer(meta, mcpDefaultServerName, cfg)
		if err := login.Save(ce.Ctx); err != nil {
			ce.Reply("Failed to save Clay MCP config: %s", err)
			return
		}
		client.invalidateMCPToolCache()
		sendMCPAuthURLNotice(client, ce, namedMCPServer{Name: mcpDefaultServerName, Config: cfg, Source: "login"})
		ce.Reply("Clay MCP needs a token. Run `!ai clay <token>` to connect.")
		return
	}

	cfg.Connected = true
	count, connectErr := client.verifyMCPServerConnection(ce.Ctx, namedMCPServer{Name: mcpDefaultServerName, Config: cfg, Source: "login"})
	if connectErr != nil {
		cfg.Connected = false
		setLoginMCPServer(meta, mcpDefaultServerName, cfg)
		if saveErr := login.Save(ce.Ctx); saveErr != nil {
			ce.Reply("Failed to save Clay MCP config: %s", saveErr)
			return
		}
		client.invalidateMCPToolCache()
		if mcpCallLikelyAuthError(connectErr) {
			sendMCPAuthURLNotice(client, ce, namedMCPServer{Name: mcpDefaultServerName, Config: cfg, Source: "login"})
		}
		ce.Reply("Failed to connect Clay MCP: %v", connectErr)
		return
	}

	setLoginMCPServer(meta, mcpDefaultServerName, cfg)
	if err := login.Save(ce.Ctx); err != nil {
		ce.Reply("Failed to save Clay MCP config: %s", err)
		return
	}
	client.invalidateMCPToolCache()
	ce.Reply("Clay MCP connected (%d tools discovered). Use `!ai agent nexus` in a room to use Nexus-only tools.", count)
}

func fnClayDisconnect(ce *commands.Event, client *AIClient) {
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

	cfg := clayMCPServerConfig(client, "")
	if !mcpServerHasTarget(cfg) {
		ce.Reply("Clay MCP is not configured")
		return
	}
	cfg.Connected = false
	setLoginMCPServer(meta, mcpDefaultServerName, cfg)
	if err := login.Save(ce.Ctx); err != nil {
		ce.Reply("Failed to disconnect Clay MCP: %s", err)
		return
	}
	client.invalidateMCPToolCache()
	ce.Reply("Clay MCP disconnected")
}

func fnClayRemove(ce *commands.Event, client *AIClient) {
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

	clearLoginMCPServer(meta, mcpDefaultServerName)
	if err := login.Save(ce.Ctx); err != nil {
		ce.Reply("Failed to remove Clay MCP login override: %s", err)
		return
	}
	client.invalidateMCPToolCache()
	ce.Reply("Clay MCP login override removed")
}
