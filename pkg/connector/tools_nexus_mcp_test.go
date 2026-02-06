package connector

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNexusAuthorizationHeaderValue(t *testing.T) {
	header, err := nexusAuthorizationHeaderValue("", "abc123")
	if err != nil {
		t.Fatalf("unexpected error for default auth type: %v", err)
	}
	if header != "Bearer abc123" {
		t.Fatalf("unexpected bearer header: %q", header)
	}

	apiHeader, err := nexusAuthorizationHeaderValue("apikey", "k1")
	if err != nil {
		t.Fatalf("unexpected error for apikey auth type: %v", err)
	}
	if apiHeader != "ApiKey k1" {
		t.Fatalf("unexpected apikey header: %q", apiHeader)
	}
}

func TestNexusMCPEndpoint(t *testing.T) {
	cfg := &NexusToolsConfig{BaseURL: "https://nexum.clay.earth"}
	if got := nexusMCPEndpoint(cfg); got != "https://nexum.clay.earth/mcp" {
		t.Fatalf("unexpected derived MCP endpoint: %q", got)
	}

	override := &NexusToolsConfig{
		BaseURL:     "https://unused.example",
		MCPEndpoint: "https://nexum.clay.earth/custom-mcp",
	}
	if got := nexusMCPEndpoint(override); got != "https://nexum.clay.earth/custom-mcp" {
		t.Fatalf("unexpected explicit MCP endpoint: %q", got)
	}
}

func TestFormatNexusMCPToolResultJSONPassthrough(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: `{"ok":true}`},
		},
	}

	formatted, err := formatNexusMCPToolResult(result)
	if err != nil {
		t.Fatalf("unexpected format error: %v", err)
	}
	if formatted != `{"ok":true}` {
		t.Fatalf("unexpected formatted output: %q", formatted)
	}
}

func TestFormatNexusMCPToolResultErrorWrap(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: `{"message":"failed"}`},
		},
		IsError: true,
	}

	formatted, err := formatNexusMCPToolResult(result)
	if err != nil {
		t.Fatalf("unexpected format error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(formatted), &parsed); err != nil {
		t.Fatalf("formatted output is not valid JSON: %v", err)
	}
	if parsed["is_error"] != true {
		t.Fatalf("expected is_error=true in wrapped output, got: %#v", parsed["is_error"])
	}
}
