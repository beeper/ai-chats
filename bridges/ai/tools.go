package ai

import (
	"context"
	"encoding/json"
	"time"

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
	ToolNameWebSearch   = toolspec.WebSearchName
	toolNameWebFetch    = toolspec.WebFetchName
	toolNameSessionInfo = toolspec.SessionInfoName
)

func BuiltinTools() []ToolDefinition {
	return []ToolDefinition{
		{Name: ToolNameWebSearch, Description: toolspec.WebSearchDescription, Parameters: toolspec.WebSearchSchema()},
		{Name: toolNameWebFetch, Description: toolspec.WebFetchDescription, Parameters: toolspec.WebFetchSchema()},
		{Name: toolNameSessionInfo, Description: toolspec.SessionInfoDescription, Parameters: toolspec.SessionInfoSchema()},
	}
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
	case toolNameSessionInfo:
		return oc.executeSessionInfoTool(ctx)
	default:
		return "Error: tool " + name + " is not available", nil
	}
}

func (oc *AIClient) isToolEnabled(meta *PortalMetadata, name string) bool {
	return name == ToolNameWebSearch || name == toolNameWebFetch || name == toolNameSessionInfo
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

func (oc *AIClient) executeSessionInfoTool(ctx context.Context) (string, error) {
	tz, loc := oc.resolveUserTimezone()
	now := time.Now().In(loc)
	payload := map[string]any{
		"timezone":    tz,
		"now":         now.Format(time.RFC3339),
		"date":        now.Format("2006-01-02"),
		"time":        now.Format("15:04:05"),
		"utc_offset":  now.Format("-07:00"),
		"unix_millis": now.UnixMilli(),
	}
	if btc := GetBridgeToolContext(ctx); btc != nil {
		if btc.Portal != nil {
			payload["room_id"] = btc.Portal.MXID.String()
			payload["portal_id"] = string(btc.Portal.ID)
		}
		if btc.SourceEventID != "" {
			payload["source_event_id"] = btc.SourceEventID.String()
		}
		if btc.SenderID != "" {
			payload["sender_id"] = btc.SenderID
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
