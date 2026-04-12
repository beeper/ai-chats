// Package tools provides the tool system for AI agents,
// including tool registration, execution, and policy enforcement.
package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/beeper/agentremote/pkg/shared/toolspec"
)

// ToolType is an alias for toolspec.ToolType for backwards compatibility.
type ToolType = toolspec.ToolType

// ToolInfo is an alias for toolspec.ToolInfo for backwards compatibility.
type ToolInfo = toolspec.ToolInfo

const (
	ToolTypeBuiltin  = toolspec.ToolTypeBuiltin
	ToolTypeProvider = toolspec.ToolTypeProvider
	ToolTypePlugin   = toolspec.ToolTypePlugin
	ToolTypeMCP      = toolspec.ToolTypeMCP
)

// Tool wraps an MCP tool with execution logic and metadata.
type Tool struct {
	mcp.Tool                                                                  // Name, Description, InputSchema
	Type     ToolType                                                         // builtin, provider, plugin, mcp
	Group    string                                                           // group:search, group:code, etc.
	PluginID string                                                           // Optional plugin id for grouping
	Execute  func(ctx context.Context, input map[string]any) (*Result, error) // nil for provider tools
}

// Result standardizes tool output with structured content blocks and metadata.
type Result struct {
	Status  ResultStatus   `json:"status"`            // success, error, partial
	Content []ContentBlock `json:"content,omitempty"` // Multi-block: text, images
	Details map[string]any `json:"details,omitempty"` // Structured metadata for parsing
	Error   string         `json:"error,omitempty"`
}

// Text returns the text content from the result.
// Returns the first text block content, or the error message if status is error.
func (r *Result) Text() string {
	if r.Status == ResultError && r.Error != "" {
		return r.Error
	}
	for _, block := range r.Content {
		if block.Type == "text" && block.Text != "" {
			return block.Text
		}
	}
	return ""
}

// ContentBlock supports multi-modal results (text, images, artifacts).
type ContentBlock struct {
	Type     string `json:"type"`               // "text", "image"
	Text     string `json:"text,omitempty"`     // For text blocks
	Data     string `json:"data,omitempty"`     // Base64 for images
	MimeType string `json:"mimeType,omitempty"` // For images
}

// ResultStatus indicates the outcome of tool execution.
type ResultStatus string

const (
	// ResultSuccess indicates the tool completed successfully.
	ResultSuccess ResultStatus = "success"
	// ResultError indicates the tool failed with an error.
	ResultError ResultStatus = "error"
)
