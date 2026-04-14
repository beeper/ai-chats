package ai

// GenerateParams contains parameters for generation requests
type GenerateParams struct {
	Model               string
	Context             PromptContext
	PreviousResponseID  string
	Temperature         *float64
	MaxCompletionTokens int
	ReasoningEffort     string // none, low, medium, high (for reasoning models)
	WebSearchEnabled    bool
}

// GenerateResponse contains the result of a non-streaming generation
type GenerateResponse struct {
	Content      string
	FinishReason string
	ResponseID   string // For Responses API continuation
	ToolCalls    []ToolCallResult
	Usage        UsageInfo
}

// StreamEventType identifies the type of streaming event
type StreamEventType string

const (
	StreamEventDelta     StreamEventType = "delta"     // Text content delta
	StreamEventReasoning StreamEventType = "reasoning" // Reasoning/thinking delta
	StreamEventToolCall  StreamEventType = "tool_call" // Tool call request
	StreamEventComplete  StreamEventType = "complete"  // Generation complete
	StreamEventError     StreamEventType = "error"     // Error occurred
)

// StreamEvent represents a single event from a streaming response
type StreamEvent struct {
	Type           StreamEventType
	Delta          string          // Text chunk for delta events
	ReasoningDelta string          // Thinking/reasoning chunk
	ToolCall       *ToolCallResult // For tool_call events
	FinishReason   string          // For complete events
	ResponseID     string          // Response ID (for Responses API)
	Usage          *UsageInfo      // Token usage (usually on complete)
	Error          error           // For error events
}

// ToolCallResult represents a tool/function call from the model
type ToolCallResult struct {
	ID        string
	Name      string
	Arguments string // JSON string of arguments
}

// UsageInfo contains token usage information
type UsageInfo struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	ReasoningTokens  int // For models with extended thinking
}

// Note: ModelInfo is defined in events.go and used for model metadata
