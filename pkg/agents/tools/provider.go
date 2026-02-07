package tools

// IsProviderTool returns true if the tool is handled by the provider API.
func IsProviderTool(t *Tool) bool {
	return t.Type == ToolTypeProvider || t.Type == ToolTypePlugin
}
