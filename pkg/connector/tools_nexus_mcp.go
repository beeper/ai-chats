package connector

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	nexusMCPDefaultPath      = "/mcp"
	nexusMCPToolCacheTTL     = 60 * time.Second
	nexusMCPDiscoveryTimeout = 3 * time.Second
)

type nexusAuthRoundTripper struct {
	base          http.RoundTripper
	authorization string
}

func (rt *nexusAuthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := rt.base
	if base == nil {
		base = http.DefaultTransport
	}
	if strings.TrimSpace(rt.authorization) == "" {
		return base.RoundTrip(req)
	}
	cloned := req.Clone(req.Context())
	cloned.Header = req.Header.Clone()
	if strings.TrimSpace(cloned.Header.Get("Authorization")) == "" {
		cloned.Header.Set("Authorization", rt.authorization)
	}
	return base.RoundTrip(cloned)
}

func mcpAuthorizationHeaderValue(authType, token string) (string, error) {
	authType = strings.ToLower(strings.TrimSpace(authType))
	if authType == "" {
		authType = "bearer"
	}
	token = strings.TrimSpace(token)
	switch authType {
	case "none":
		return "", nil
	case "bearer":
		if token == "" {
			return "", errors.New("missing MCP token")
		}
		return "Bearer " + token, nil
	case "apikey", "api_key", "api-key":
		if token == "" {
			return "", errors.New("missing MCP token")
		}
		return "ApiKey " + token, nil
	default:
		return "", fmt.Errorf("unsupported MCP auth_type %q", authType)
	}
}

func nexusMCPEndpoint(cfg *NexusToolsConfig) string {
	if cfg == nil {
		return ""
	}
	if explicit := strings.TrimSpace(cfg.MCPEndpoint); explicit != "" {
		return strings.TrimRight(explicit, "/")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(baseURL), nexusMCPDefaultPath) {
		return baseURL
	}
	return baseURL + nexusMCPDefaultPath
}

func copyToolDefinitions(defs []ToolDefinition) []ToolDefinition {
	if len(defs) == 0 {
		return nil
	}
	out := make([]ToolDefinition, len(defs))
	copy(out, defs)
	return out
}

func copyStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	return maps.Clone(src)
}

func (oc *AIClient) nexusRequestTimeout() time.Duration {
	timeoutSeconds := defaultNexusTimeoutSeconds
	if oc != nil && oc.connector != nil && oc.connector.Config.Tools.Nexus != nil && oc.connector.Config.Tools.Nexus.TimeoutSeconds > 0 {
		timeoutSeconds = oc.connector.Config.Tools.Nexus.TimeoutSeconds
	}
	return time.Duration(timeoutSeconds) * time.Second
}

func (oc *AIClient) mcpHTTPClientForServer(server namedMCPServer) (*http.Client, error) {
	headerValue, err := mcpAuthorizationHeaderValue(server.Config.AuthType, server.Config.Token)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout: oc.nexusRequestTimeout(),
		Transport: &nexusAuthRoundTripper{
			base:          http.DefaultTransport,
			authorization: headerValue,
		},
	}
	return client, nil
}

func (oc *AIClient) newNexusMCPSession(ctx context.Context, server namedMCPServer) (*mcp.ClientSession, error) {
	if oc == nil {
		return nil, fmt.Errorf("mcp requires bridge context")
	}
	server.Config = normalizeMCPServerConfig(server.Config)
	if !mcpServerHasTarget(server.Config) {
		return nil, fmt.Errorf("MCP server %q has no target", server.Name)
	}
	if !server.Config.Connected {
		return nil, fmt.Errorf("MCP server %q is disconnected", server.Name)
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "ai-bridge",
		Version: "1.0.0",
	}, nil)
	var (
		session *mcp.ClientSession
		err     error
	)
	switch server.Config.Transport {
	case mcpTransportStdio:
		cmd := exec.CommandContext(ctx, server.Config.Command, server.Config.Args...)
		session, err = client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	case mcpTransportStreamableHTTP:
		httpClient, clientErr := oc.mcpHTTPClientForServer(server)
		if clientErr != nil {
			return nil, clientErr
		}
		session, err = client.Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint:   server.Config.Endpoint,
			HTTPClient: httpClient,
			MaxRetries: 1,
		}, nil)
	default:
		return nil, fmt.Errorf("unsupported MCP transport %q", server.Config.Transport)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to connect MCP server %q (%s): %w", server.Name, mcpServerTargetLabel(server.Config), err)
	}
	return session, nil
}

func (oc *AIClient) fetchNexusMCPToolDefinitionsForServer(ctx context.Context, server namedMCPServer) ([]ToolDefinition, error) {
	session, err := oc.newNexusMCPSession(ctx, server)
	if err != nil {
		return nil, err
	}
	defer session.Close()

	toolsResult, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list MCP tools from %s: %w", server.Name, err)
	}
	if toolsResult == nil || len(toolsResult.Tools) == 0 {
		return nil, nil
	}

	seen := make(map[string]struct{}, len(toolsResult.Tools))
	defs := make([]ToolDefinition, 0, len(toolsResult.Tools))
	for _, tool := range toolsResult.Tools {
		if tool == nil {
			continue
		}
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}

		description := strings.TrimSpace(tool.Description)
		if description == "" && tool.Annotations != nil {
			description = strings.TrimSpace(tool.Annotations.Title)
		}
		defs = append(defs, ToolDefinition{
			Name:        name,
			Description: description,
			Parameters:  toolSchemaToMap(tool.InputSchema),
		})
	}

	slices.SortFunc(defs, func(a, b ToolDefinition) int {
		return cmp.Compare(a.Name, b.Name)
	})

	return defs, nil
}

func (oc *AIClient) nexusMCPToolDefinitions(ctx context.Context) ([]ToolDefinition, error) {
	if !oc.isMCPConfigured() {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	now := time.Now()
	oc.nexusMCPToolsMu.Lock()
	if now.Sub(oc.nexusMCPToolsFetchedAt) < nexusMCPToolCacheTTL {
		cached := copyToolDefinitions(oc.nexusMCPTools)
		oc.nexusMCPToolsMu.Unlock()
		return cached, nil
	}
	oc.nexusMCPToolsMu.Unlock()

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

	servers := oc.activeMCPServers()
	combined := make([]ToolDefinition, 0)
	toolSet := make(map[string]struct{})
	toolServer := make(map[string]string)
	var firstErr error

	for _, server := range servers {
		defs, err := oc.fetchNexusMCPToolDefinitionsForServer(callCtx, server)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			oc.loggerForContext(ctx).Debug().Err(err).Str("mcp_server", server.Name).Msg("Failed to discover MCP tools from server")
			continue
		}
		for _, def := range defs {
			if _, exists := toolSet[def.Name]; exists {
				continue
			}
			toolSet[def.Name] = struct{}{}
			toolServer[def.Name] = server.Name
			combined = append(combined, def)
		}
	}

	slices.SortFunc(combined, func(a, b ToolDefinition) int {
		return cmp.Compare(a.Name, b.Name)
	})

	oc.nexusMCPToolsMu.Lock()
	oc.nexusMCPTools = copyToolDefinitions(combined)
	oc.nexusMCPToolSet = toolSet
	oc.nexusMCPToolServer = copyStringMap(toolServer)
	oc.nexusMCPToolsFetchedAt = time.Now()
	oc.nexusMCPToolsMu.Unlock()

	if len(combined) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return combined, nil
}

func (oc *AIClient) nexusDiscoveredToolNames(ctx context.Context) []string {
	defs, err := oc.nexusMCPToolDefinitions(ctx)
	if err != nil || len(defs) == 0 {
		return nil
	}
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	return names
}

func (oc *AIClient) hasCachedNexusMCPTool(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || oc == nil {
		return false
	}
	oc.nexusMCPToolsMu.Lock()
	defer oc.nexusMCPToolsMu.Unlock()
	if oc.nexusMCPToolSet == nil {
		return false
	}
	_, ok := oc.nexusMCPToolSet[name]
	return ok
}

func (oc *AIClient) cachedNexusMCPServerForTool(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || oc == nil {
		return ""
	}
	oc.nexusMCPToolsMu.Lock()
	defer oc.nexusMCPToolsMu.Unlock()
	if oc.nexusMCPToolServer == nil {
		return ""
	}
	return strings.TrimSpace(oc.nexusMCPToolServer[name])
}

func (oc *AIClient) lookupNexusMCPToolDefinition(ctx context.Context, name string) (ToolDefinition, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ToolDefinition{}, false
	}
	defs, err := oc.nexusMCPToolDefinitions(ctx)
	if err != nil {
		return ToolDefinition{}, false
	}
	for _, def := range defs {
		if def.Name == name {
			return def, true
		}
	}
	return ToolDefinition{}, false
}

func (oc *AIClient) nexusMCPServerByName(name string) (namedMCPServer, bool) {
	name = normalizeMCPServerName(name)
	for _, server := range oc.activeMCPServers() {
		if server.Name == name {
			return server, true
		}
	}
	return namedMCPServer{}, false
}

func (oc *AIClient) nexusMCPServerForTool(ctx context.Context, toolName string) (namedMCPServer, bool) {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return namedMCPServer{}, false
	}
	if isNexusToolName(toolName) {
		servers := oc.activeNexusMCPServers()
		if len(servers) == 0 {
			return namedMCPServer{}, false
		}
		return servers[0], true
	}
	if serverName := oc.cachedNexusMCPServerForTool(toolName); serverName != "" {
		if server, ok := oc.nexusMCPServerByName(serverName); ok {
			return server, true
		}
	}
	if _, ok := oc.lookupNexusMCPToolDefinition(ctx, toolName); ok {
		if serverName := oc.cachedNexusMCPServerForTool(toolName); serverName != "" {
			if server, ok := oc.nexusMCPServerByName(serverName); ok {
				return server, true
			}
		}
	}
	servers := oc.activeMCPServers()
	if len(servers) == 0 {
		return namedMCPServer{}, false
	}
	return servers[0], true
}

func (oc *AIClient) isMCPToolName(name string) bool {
	if isNexusToolName(name) {
		return true
	}
	return oc.hasCachedNexusMCPTool(name)
}

func (oc *AIClient) isNexusScopedMCPTool(name string) bool {
	if isNexusToolName(name) {
		return true
	}
	serverName := oc.cachedNexusMCPServerForTool(name)
	if serverName == "" {
		return false
	}
	server, ok := oc.configuredMCPServerByName(serverName)
	if !ok {
		return false
	}
	return normalizeMCPServerKind(server.Config.Kind) == mcpServerKindNexus
}

func (oc *AIClient) shouldUseNexusMCPTool(ctx context.Context, toolName string) bool {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" || !oc.isMCPConfigured() {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if isNexusToolName(toolName) {
		return true
	}
	if oc.hasCachedNexusMCPTool(toolName) {
		return true
	}

	discoveryCtx, cancel := context.WithTimeout(ctx, nexusMCPDiscoveryTimeout)
	defer cancel()
	_, ok := oc.lookupNexusMCPToolDefinition(discoveryCtx, toolName)
	return ok
}

func formatNexusMCPToolResult(result *mcp.CallToolResult) (string, error) {
	if result == nil {
		return "{}", nil
	}

	if len(result.Content) == 1 {
		if textContent, ok := result.Content[0].(*mcp.TextContent); ok {
			text := strings.TrimSpace(textContent.Text)
			if text != "" {
				if json.Valid([]byte(text)) {
					if !result.IsError {
						return text, nil
					}
					var parsed any
					if err := json.Unmarshal([]byte(text), &parsed); err == nil {
						wrapped, marshalErr := json.Marshal(map[string]any{
							"is_error": true,
							"data":     parsed,
						})
						if marshalErr == nil {
							return string(wrapped), nil
						}
					}
				}
				wrapped, err := json.Marshal(map[string]any{
					"is_error": result.IsError,
					"text":     text,
				})
				if err != nil {
					return "", fmt.Errorf("failed to encode Nexus MCP text result: %w", err)
				}
				return string(wrapped), nil
			}
		}
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("failed to encode Nexus MCP result: %w", err)
	}
	trimmed := strings.TrimSpace(string(encoded))
	if trimmed == "" {
		return "{}", nil
	}
	return trimmed, nil
}

func mcpCallLikelyAuthError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(text, "401") ||
		strings.Contains(text, "403") ||
		strings.Contains(text, "unauthorized") ||
		strings.Contains(text, "forbidden") ||
		strings.Contains(text, "missing mcp token")
}

func (oc *AIClient) notifyMCPAuthURL(ctx context.Context, server namedMCPServer) {
	if strings.TrimSpace(server.Config.AuthURL) == "" {
		return
	}
	btc := GetBridgeToolContext(ctx)
	if btc == nil || btc.Client == nil || btc.Portal == nil {
		return
	}
	btc.Client.sendSystemNotice(ctx, btc.Portal, fmt.Sprintf("MCP authentication required for server '%s'. Open this URL: %s", server.Name, server.Config.AuthURL))
}

func (oc *AIClient) executeNexusMCPTool(ctx context.Context, toolName string, args map[string]any) (string, error) {
	if !oc.isMCPConfigured() {
		return "", fmt.Errorf("MCP tools are not configured (add an MCP server with !ai mcp add/connect)")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	callCtx := ctx
	var cancel context.CancelFunc
	if _, hasDeadline := callCtx.Deadline(); !hasDeadline {
		callCtx, cancel = context.WithTimeout(ctx, oc.nexusRequestTimeout())
	}
	if cancel != nil {
		defer cancel()
	}

	server, ok := oc.nexusMCPServerForTool(callCtx, toolName)
	if !ok {
		return "", fmt.Errorf("no connected MCP server available for tool %s", toolName)
	}

	session, err := oc.newNexusMCPSession(callCtx, server)
	if err != nil {
		if mcpCallLikelyAuthError(err) {
			oc.notifyMCPAuthURL(callCtx, server)
		}
		return "", err
	}
	defer session.Close()

	result, err := session.CallTool(callCtx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	})
	if err != nil {
		if mcpCallLikelyAuthError(err) {
			oc.notifyMCPAuthURL(callCtx, server)
		}
		return "", fmt.Errorf("MCP call failed for %s on %s: %w", toolName, server.Name, err)
	}
	return formatNexusMCPToolResult(result)
}

func (oc *AIClient) enabledBuiltinToolsForModel(ctx context.Context, meta *PortalMetadata) []ToolDefinition {
	mcpTools, err := oc.nexusMCPToolDefinitions(ctx)
	if err != nil {
		oc.loggerForContext(ctx).Debug().Err(err).Msg("Failed to discover Nexus MCP tools")
		mcpTools = nil
	}

	mcpByName := make(map[string]ToolDefinition, len(mcpTools))
	for _, tool := range mcpTools {
		mcpByName[tool.Name] = tool
	}

	builtinTools := BuiltinTools()
	enabled := make([]ToolDefinition, 0, len(builtinTools)+len(mcpTools))
	seen := make(map[string]struct{}, len(builtinTools)+len(mcpTools))

	for _, tool := range builtinTools {
		if !oc.isToolEnabled(meta, tool.Name) {
			continue
		}
		if mcpTool, ok := mcpByName[tool.Name]; ok {
			enabled = append(enabled, mcpTool)
			seen[mcpTool.Name] = struct{}{}
			delete(mcpByName, tool.Name)
			continue
		}
		enabled = append(enabled, tool)
		seen[tool.Name] = struct{}{}
	}

	for _, tool := range mcpTools {
		if _, ok := seen[tool.Name]; ok {
			continue
		}
		if !oc.isToolEnabled(meta, tool.Name) {
			continue
		}
		enabled = append(enabled, tool)
		seen[tool.Name] = struct{}{}
	}

	return enabled
}
