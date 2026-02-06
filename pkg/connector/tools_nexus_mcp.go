package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
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

func normalizeNexusAuthType(authType string) string {
	value := strings.ToLower(strings.TrimSpace(authType))
	if value == "" {
		return "bearer"
	}
	switch value {
	case "bearer", "apikey", "api_key", "api-key":
		return value
	default:
		return value
	}
}

func nexusAuthorizationHeaderValue(authType, token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", errors.New("missing Nexus token")
	}
	switch normalizeNexusAuthType(authType) {
	case "bearer":
		return "Bearer " + token, nil
	case "apikey", "api_key", "api-key":
		return "ApiKey " + token, nil
	default:
		return "", fmt.Errorf("unsupported network.tools.nexus.auth_type %q", authType)
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

func (oc *AIClient) nexusRequestTimeout() time.Duration {
	timeoutSeconds := defaultNexusTimeoutSeconds
	if oc != nil && oc.connector != nil && oc.connector.Config.Tools.Nexus != nil && oc.connector.Config.Tools.Nexus.TimeoutSeconds > 0 {
		timeoutSeconds = oc.connector.Config.Tools.Nexus.TimeoutSeconds
	}
	return time.Duration(timeoutSeconds) * time.Second
}

func (oc *AIClient) nexusMCPHTTPClient(cfg *NexusToolsConfig) (*http.Client, error) {
	headerValue, err := nexusAuthorizationHeaderValue(cfg.AuthType, cfg.Token)
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

func (oc *AIClient) newNexusMCPSession(ctx context.Context) (*mcp.ClientSession, error) {
	if oc == nil || oc.connector == nil {
		return nil, fmt.Errorf("nexus mcp requires bridge context")
	}
	cfg := oc.connector.Config.Tools.Nexus
	if !nexusConfigured(cfg) {
		return nil, fmt.Errorf("nexus tools are not configured (set network.tools.nexus.base_url or mcp_endpoint and token)")
	}

	endpoint := nexusMCPEndpoint(cfg)
	if endpoint == "" {
		return nil, fmt.Errorf("nexus MCP endpoint is empty")
	}
	httpClient, err := oc.nexusMCPHTTPClient(cfg)
	if err != nil {
		return nil, err
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "ai-bridge",
		Version: "1.0.0",
	}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: httpClient,
		MaxRetries: 1,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Nexus MCP endpoint %q: %w", endpoint, err)
	}
	return session, nil
}

func (oc *AIClient) fetchNexusMCPToolDefinitions(ctx context.Context) ([]ToolDefinition, error) {
	session, err := oc.newNexusMCPSession(ctx)
	if err != nil {
		return nil, err
	}
	defer session.Close()

	toolsResult, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list Nexus MCP tools: %w", err)
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

	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Name < defs[j].Name
	})

	return defs, nil
}

func (oc *AIClient) nexusMCPToolDefinitions(ctx context.Context) ([]ToolDefinition, error) {
	if !oc.isNexusConfigured() {
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

	defs, err := oc.fetchNexusMCPToolDefinitions(callCtx)
	if err != nil {
		return nil, err
	}

	toolSet := make(map[string]struct{}, len(defs))
	for _, def := range defs {
		toolSet[def.Name] = struct{}{}
	}

	oc.nexusMCPToolsMu.Lock()
	oc.nexusMCPTools = copyToolDefinitions(defs)
	oc.nexusMCPToolSet = toolSet
	oc.nexusMCPToolsFetchedAt = time.Now()
	oc.nexusMCPToolsMu.Unlock()

	return defs, nil
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

func (oc *AIClient) isNexusMCPToolName(name string) bool {
	if isNexusToolName(name) {
		return true
	}
	return oc.hasCachedNexusMCPTool(name)
}

func (oc *AIClient) shouldUseNexusMCPTool(ctx context.Context, toolName string) bool {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" || !oc.isNexusConfigured() {
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

func (oc *AIClient) executeNexusMCPTool(ctx context.Context, toolName string, args map[string]any) (string, error) {
	if !oc.isNexusConfigured() {
		return "", fmt.Errorf("nexus tools are not configured (set network.tools.nexus.base_url or mcp_endpoint and token)")
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

	session, err := oc.newNexusMCPSession(callCtx)
	if err != nil {
		return "", err
	}
	defer session.Close()

	result, err := session.CallTool(callCtx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	})
	if err != nil {
		return "", fmt.Errorf("Nexus MCP call failed for %s: %w", toolName, err)
	}
	return formatNexusMCPToolResult(result)
}

func (oc *AIClient) enabledBuiltinToolsForModel(ctx context.Context, meta *PortalMetadata) []ToolDefinition {
	mcpTools, err := oc.nexusMCPToolDefinitions(ctx)
	if err != nil {
		oc.log.Debug().Err(err).Msg("Failed to discover Nexus MCP tools")
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
