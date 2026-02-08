package connector

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPAuthorizationHeaderValue(t *testing.T) {
	header, err := mcpAuthorizationHeaderValue("", "abc123")
	if err != nil {
		t.Fatalf("unexpected error for default auth type: %v", err)
	}
	if header != "Bearer abc123" {
		t.Fatalf("unexpected bearer header: %q", header)
	}

	apiHeader, err := mcpAuthorizationHeaderValue("apikey", "k1")
	if err != nil {
		t.Fatalf("unexpected error for apikey auth type: %v", err)
	}
	if apiHeader != "ApiKey k1" {
		t.Fatalf("unexpected apikey header: %q", apiHeader)
	}
}

func TestMCPEndpointFromNexusConfig(t *testing.T) {
	cfg := &NexusToolsConfig{BaseURL: "https://nexum.clay.earth"}
	if got := mcpEndpointFromNexusConfig(cfg); got != "https://nexum.clay.earth/mcp" {
		t.Fatalf("unexpected derived MCP endpoint: %q", got)
	}

	override := &NexusToolsConfig{
		BaseURL:     "https://unused.example",
		MCPEndpoint: "https://nexum.clay.earth/custom-mcp",
	}
	if got := mcpEndpointFromNexusConfig(override); got != "https://nexum.clay.earth/custom-mcp" {
		t.Fatalf("unexpected explicit MCP endpoint: %q", got)
	}
}

func TestFormatMCPToolResultJSONPassthrough(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: `{"ok":true}`},
		},
	}

	formatted, err := formatMCPToolResult(result)
	if err != nil {
		t.Fatalf("unexpected format error: %v", err)
	}
	if formatted != `{"ok":true}` {
		t.Fatalf("unexpected formatted output: %q", formatted)
	}
}

func TestFormatMCPToolResultErrorWrap(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: `{"message":"failed"}`},
		},
		IsError: true,
	}

	formatted, err := formatMCPToolResult(result)
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

func TestFormatMCPToolResultImageContent(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.ImageContent{MIMEType: "image/png", Data: []byte("testdata")},
		},
	}

	formatted, err := formatMCPToolResult(result)
	if err != nil {
		t.Fatalf("unexpected format error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(formatted), &parsed); err != nil {
		t.Fatalf("formatted output is not valid JSON: %v", err)
	}
	content, ok := parsed["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("expected content array with 1 item, got: %v", parsed)
	}
	item := content[0].(map[string]any)
	if item["type"] != "image" {
		t.Fatalf("expected type=image, got: %v", item["type"])
	}
	if item["mimeType"] != "image/png" {
		t.Fatalf("expected mimeType=image/png, got: %v", item["mimeType"])
	}
}

func TestFormatMCPToolResultMultiContent(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "hello"},
			&mcp.TextContent{Text: "world"},
		},
	}

	formatted, err := formatMCPToolResult(result)
	if err != nil {
		t.Fatalf("unexpected format error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(formatted), &parsed); err != nil {
		t.Fatalf("formatted output is not valid JSON: %v", err)
	}
	content, ok := parsed["content"].([]any)
	if !ok || len(content) != 2 {
		t.Fatalf("expected content array with 2 items, got: %v", parsed)
	}
}
