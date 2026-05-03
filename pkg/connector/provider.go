package connector

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
