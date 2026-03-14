package runtime

import "github.com/openai/openai-go/v3"

// MessageSummary is a generic message summary.
type MessageSummary struct {
	Role string
	Body string
}

// AssistantMessageInfo is a generic assistant response.
type AssistantMessageInfo struct {
	Body             string
	Model            string
	PromptTokens     int64
	CompletionTokens int64
}

// CompletionToolCall represents a tool call from a model completion.
type CompletionToolCall struct {
	ID       string
	Name     string
	ArgsJSON string
}

// CompletionResult represents a model completion response.
type CompletionResult struct {
	AssistantMessage openai.ChatCompletionMessageParamUnion
	ToolCalls        []CompletionToolCall
	Done             bool
}

// SessionPortalInfo is a generic portal reference for session listing.
type SessionPortalInfo struct {
	Key       string
	PortalKey any
}
