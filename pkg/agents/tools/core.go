package tools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
)

var (
	MessageTool = &Tool{
		Tool: mcp.Tool{
			Name:        toolspec.MessageName,
			Description: toolspec.MessageDescription,
			Annotations: &mcp.ToolAnnotations{Title: "Message"},
			InputSchema: toolspec.MessageSchema(),
		},
		Type:  ToolTypeBuiltin,
		Group: GroupMessaging,
	}
	WebFetchTool = &Tool{
		Tool: mcp.Tool{
			Name:        toolspec.WebFetchName,
			Description: toolspec.WebFetchDescription,
			Annotations: &mcp.ToolAnnotations{Title: "Web Fetch"},
			InputSchema: toolspec.WebFetchSchema(),
		},
		Type:  ToolTypeBuiltin,
		Group: GroupWeb,
	}
	SessionStatusTool = &Tool{
		Tool: mcp.Tool{
			Name:        toolspec.SessionStatusName,
			Description: toolspec.SessionStatusDescription,
			Annotations: &mcp.ToolAnnotations{Title: "Session Status"},
			InputSchema: toolspec.SessionStatusSchema(),
		},
		Type:  ToolTypeBuiltin,
		Group: GroupStatus,
	}
	MemorySearchTool = &Tool{
		Tool: mcp.Tool{
			Name:        toolspec.MemorySearchName,
			Description: toolspec.MemorySearchDescription,
			Annotations: &mcp.ToolAnnotations{Title: "Memory Search"},
			InputSchema: toolspec.MemorySearchSchema(),
		},
		Type:  ToolTypeBuiltin,
		Group: GroupMemory,
	}
	MemoryGetTool = &Tool{
		Tool: mcp.Tool{
			Name:        toolspec.MemoryGetName,
			Description: toolspec.MemoryGetDescription,
			Annotations: &mcp.ToolAnnotations{Title: "Memory Get"},
			InputSchema: toolspec.MemoryGetSchema(),
		},
		Type:  ToolTypeBuiltin,
		Group: GroupMemory,
	}
	ImageTool = &Tool{
		Tool: mcp.Tool{
			Name:        toolspec.ImageName,
			Description: toolspec.ImageDescription,
			Annotations: &mcp.ToolAnnotations{Title: "Image"},
			InputSchema: toolspec.ImageSchema(),
		},
		Type:  ToolTypeBuiltin,
		Group: GroupMedia,
	}
	ImageGenerateTool = &Tool{
		Tool: mcp.Tool{
			Name:        toolspec.ImageGenerateName,
			Description: toolspec.ImageGenerateDescription,
			Annotations: &mcp.ToolAnnotations{Title: "Image Generate"},
			InputSchema: toolspec.ImageGenerateSchema(),
		},
		Type:  ToolTypeBuiltin,
		Group: GroupMedia,
	}
	TTSTool = &Tool{
		Tool: mcp.Tool{
			Name:        toolspec.TTSName,
			Description: toolspec.TTSDescription,
			Annotations: &mcp.ToolAnnotations{Title: "TTS"},
			InputSchema: toolspec.TTSSchema(),
		},
		Type:  ToolTypeBuiltin,
		Group: GroupMedia,
	}
	GravatarFetchTool = &Tool{
		Tool: mcp.Tool{
			Name:        toolspec.GravatarFetchName,
			Description: toolspec.GravatarFetchDescription,
			Annotations: &mcp.ToolAnnotations{Title: "Gravatar Fetch"},
			InputSchema: toolspec.GravatarFetchSchema(),
		},
		Type:  ToolTypeBuiltin,
		Group: GroupOpenClaw,
	}
)
