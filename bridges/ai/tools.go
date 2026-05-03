package ai

import (
	"context"
	"encoding/json"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/shared/toolspec"
)

type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type BridgeToolContext struct {
	Client        *AIClient
	Portal        *bridgev2.Portal
	Meta          *PortalMetadata
	SourceEventID id.EventID
	SenderID      string
}

type bridgeToolContextKey struct{}

func WithBridgeToolContext(ctx context.Context, btc *BridgeToolContext) context.Context {
	return context.WithValue(ctx, bridgeToolContextKey{}, btc)
}

func GetBridgeToolContext(ctx context.Context) *BridgeToolContext {
	return contextValue[*BridgeToolContext](ctx, bridgeToolContextKey{})
}

const (
	ToolNameWebSearch = toolspec.WebSearchName
	toolNameWebFetch  = toolspec.WebFetchName
)

func BuiltinTools() []ToolDefinition {
	return []ToolDefinition{
		{Name: ToolNameWebSearch, Description: toolspec.WebSearchDescription, Parameters: toolspec.WebSearchSchema()},
		{Name: toolNameWebFetch, Description: toolspec.WebFetchDescription, Parameters: toolspec.WebFetchSchema()},
	}
}

func GetBuiltinTool(name string) *ToolDefinition {
	for _, tool := range BuiltinTools() {
		if tool.Name == name {
			copy := tool
			return &copy
		}
	}
	return nil
}

func (oc *AIClient) executeBuiltinTool(ctx context.Context, portal *bridgev2.Portal, name string, argsJSON string) (string, error) {
	var args map[string]any
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", err
		}
	}
	switch name {
	case ToolNameWebSearch:
		return executeWebSearchWithProviders(ctx, args)
	case toolNameWebFetch:
		return executeWebFetchWithProviders(ctx, args)
	default:
		return "Error: tool " + name + " is not available", nil
	}
}

func (oc *AIClient) isToolEnabled(meta *PortalMetadata, name string) bool {
	return name == ToolNameWebSearch || name == toolNameWebFetch
}

func (oc *AIClient) toolNamesForPortal(meta *PortalMetadata) []string {
	return []string{ToolNameWebSearch, toolNameWebFetch}
}

func (oc *AIClient) toolDescriptionForPortal(_ *PortalMetadata, _ string, fallback string) string {
	return fallback
}

func (oc *AIClient) isToolAvailable(_ *PortalMetadata, name string) (bool, SettingSource, string) {
	return oc.isToolEnabled(nil, name), SourceGlobalDefault, ""
}

func (oc *AIClient) enabledBuiltinToolsForModel(ctx context.Context, meta *PortalMetadata) []ToolDefinition {
	return BuiltinTools()
}

func (oc *AIClient) selectedBuiltinToolsForTurn(ctx context.Context, meta *PortalMetadata) []ToolDefinition {
	if !oc.modelSupportsToolCalling(ctx, meta) {
		return nil
	}
	return oc.enabledBuiltinToolsForModel(ctx, meta)
}
