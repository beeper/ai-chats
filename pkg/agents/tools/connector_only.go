package tools

// newConnectorOnlyTool creates a builtin tool that is only executable through
// the connector runtime, not the local tool executor.
func newConnectorOnlyTool(name, description, title string, schema map[string]any) *Tool {
	return newUnavailableTool(name, description, title, schema, GroupWeb, name+" is only available through the connector")
}
