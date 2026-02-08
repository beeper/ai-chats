package connector

import (
	"context"
	"fmt"
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
)

func isNexusCompactToolName(name string) bool {
	return strings.TrimSpace(name) == toolspec.NexusContactsName
}

type nexusDispatchTarget struct {
	toolName string
	route    string
}

var nexusContactsActionTargets = map[string]nexusDispatchTarget{
	// Canonical compact actions.
	"search":          {toolName: toolspec.NexusSearchContactsName, route: nexusToolRoutes[toolspec.NexusSearchContactsName]},
	"get":             {toolName: toolspec.NexusGetContactName, route: nexusToolRoutes[toolspec.NexusGetContactName]},
	"create":          {toolName: toolspec.NexusCreateContactName, route: nexusToolRoutes[toolspec.NexusCreateContactName]},
	"update":          {toolName: toolspec.NexusUpdateContactName, route: nexusToolRoutes[toolspec.NexusUpdateContactName]},
	"note":            {toolName: toolspec.NexusCreateNoteName, route: nexusToolRoutes[toolspec.NexusCreateNoteName]},
	"find_duplicates": {toolName: toolspec.NexusFindDuplicatesName, route: nexusToolRoutes[toolspec.NexusFindDuplicatesName]},

	// Accept underlying tool names as actions too (handy during transition).
	"searchcontacts": {toolName: toolspec.NexusSearchContactsName, route: nexusToolRoutes[toolspec.NexusSearchContactsName]},
	"getcontact":     {toolName: toolspec.NexusGetContactName, route: nexusToolRoutes[toolspec.NexusGetContactName]},
	"createcontact":  {toolName: toolspec.NexusCreateContactName, route: nexusToolRoutes[toolspec.NexusCreateContactName]},
	"updatecontact":  {toolName: toolspec.NexusUpdateContactName, route: nexusToolRoutes[toolspec.NexusUpdateContactName]},
	"createnote":     {toolName: toolspec.NexusCreateNoteName, route: nexusToolRoutes[toolspec.NexusCreateNoteName]},
	"findduplicates": {toolName: toolspec.NexusFindDuplicatesName, route: nexusToolRoutes[toolspec.NexusFindDuplicatesName]},
}

func executeNexusContacts(ctx context.Context, args map[string]any) (string, error) {
	raw := ""
	if v, ok := args["action"].(string); ok {
		raw = v
	} else if v, ok := args["op"].(string); ok {
		raw = v
	}
	action := strings.ToLower(strings.TrimSpace(raw))
	if action == "" {
		return "", fmt.Errorf("%s: missing required field \"action\"", toolspec.NexusContactsName)
	}

	switch action {
	case "archive", "restore", "merge", "archive_contact", "restore_contact", "merge_contacts", "mergecontacts":
		return "", fmt.Errorf("%s: action %q is disabled", toolspec.NexusContactsName, raw)
	}

	target, ok := nexusContactsActionTargets[action]
	if !ok || strings.TrimSpace(target.toolName) == "" {
		return "", fmt.Errorf("%s: unknown action %q", toolspec.NexusContactsName, raw)
	}

	payload := make(map[string]any, len(args))
	for k, v := range args {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "action", "op":
			continue
		default:
			payload[k] = v
		}
	}

	if btc := GetBridgeToolContext(ctx); btc != nil && btc.Client != nil {
		// Match legacy behavior: if Nexus MCP is configured, prefer MCP execution.
		if btc.Client.shouldUseMCPTool(ctx, target.toolName) {
			return btc.Client.executeMCPTool(ctx, target.toolName, payload)
		}
	}
	if strings.TrimSpace(target.route) == "" {
		return "", fmt.Errorf("%s: missing route for action %q", toolspec.NexusContactsName, raw)
	}
	return executeNexusRoute(ctx, target.route, payload)
}
