package tools

import "github.com/beeper/agentremote/pkg/shared/toolspec"

// PluginIDForTool returns the plugin id for a tool when available.
// This is used for AgentRemote-style plugin group expansion.
func PluginIDForTool(tool *Tool) (string, bool) {
	if tool == nil {
		return "", false
	}
	if tool.PluginID != "" {
		return tool.PluginID, true
	}
	if tool.Type == toolspec.ToolTypePlugin {
		return tool.Name, tool.Name != ""
	}
	return "", false
}

// IsPluginTool reports whether the tool is a plugin tool.
func IsPluginTool(tool *Tool) bool {
	return tool != nil && tool.Type == toolspec.ToolTypePlugin
}
