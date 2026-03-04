package ai

import "context"

type Api string

const (
	APIOpenAICompletions   Api = "openai-completions"
	APIOpenAIResponses     Api = "openai-responses"
	APIAzureOpenAIResponse Api = "azure-openai-responses"
	APIOpenAICodexResponse Api = "openai-codex-responses"
	APIAnthropicMessages   Api = "anthropic-messages"
	APIBedrockConverse     Api = "bedrock-converse-stream"
	APIGoogleGenerativeAI  Api = "google-generative-ai"
	APIGoogleGeminiCLI     Api = "google-gemini-cli"
	APIGoogleVertex        Api = "google-vertex"
)

type Provider string

type ThinkingLevel string

const (
	ThinkingMinimal ThinkingLevel = "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
	ThinkingXHigh   ThinkingLevel = "xhigh"
)

type ThinkingBudgets struct {
	Minimal int
	Low     int
	Medium  int
	High    int
}

type CacheRetention string

const (
	CacheRetentionNone  CacheRetention = "none"
	CacheRetentionShort CacheRetention = "short"
	CacheRetentionLong  CacheRetention = "long"
)

type Transport string

const (
	TransportSSE       Transport = "sse"
	TransportWebSocket Transport = "websocket"
	TransportAuto      Transport = "auto"
)

type StreamOptions struct {
	Temperature     *float64
	MaxTokens       int
	Ctx             context.Context
	APIKey          string
	Transport       Transport
	CacheRetention  CacheRetention
	SessionID       string
	OnPayload       func(any)
	Headers         map[string]string
	MaxRetryDelayMs int
	Metadata        map[string]any
}

type SimpleStreamOptions struct {
	StreamOptions
	Reasoning       ThinkingLevel
	ThinkingBudgets ThinkingBudgets
}

type ContentType string

const (
	ContentTypeText     ContentType = "text"
	ContentTypeThinking ContentType = "thinking"
	ContentTypeImage    ContentType = "image"
	ContentTypeToolCall ContentType = "toolCall"
)

type ContentBlock struct {
	Type ContentType `json:"type"`

	Text          string `json:"text,omitempty"`
	TextSignature string `json:"textSignature,omitempty"`

	Thinking          string `json:"thinking,omitempty"`
	ThinkingSignature string `json:"thinkingSignature,omitempty"`
	Redacted          bool   `json:"redacted,omitempty"`

	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`

	ID               string         `json:"id,omitempty"`
	Name             string         `json:"name,omitempty"`
	Arguments        map[string]any `json:"arguments,omitempty"`
	ThoughtSignature string         `json:"thoughtSignature,omitempty"`
}

type UsageCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

type Usage struct {
	Input       int       `json:"input"`
	Output      int       `json:"output"`
	CacheRead   int       `json:"cacheRead"`
	CacheWrite  int       `json:"cacheWrite"`
	TotalTokens int       `json:"totalTokens"`
	Cost        UsageCost `json:"cost"`
}

type StopReason string

const (
	StopReasonStop    StopReason = "stop"
	StopReasonLength  StopReason = "length"
	StopReasonToolUse StopReason = "toolUse"
	StopReasonError   StopReason = "error"
	StopReasonAborted StopReason = "aborted"
)

type MessageRole string

const (
	RoleUser       MessageRole = "user"
	RoleAssistant  MessageRole = "assistant"
	RoleToolResult MessageRole = "toolResult"
)

type Message struct {
	Role MessageRole `json:"role"`

	// user message can be string or blocks
	Text    string         `json:"text,omitempty"`
	Content []ContentBlock `json:"content,omitempty"`

	// assistant metadata
	API          Api        `json:"api,omitempty"`
	Provider     Provider   `json:"provider,omitempty"`
	Model        string     `json:"model,omitempty"`
	Usage        Usage      `json:"usage,omitempty"`
	StopReason   StopReason `json:"stopReason,omitempty"`
	ErrorMessage string     `json:"errorMessage,omitempty"`

	// toolResult metadata
	ToolCallID string `json:"toolCallId,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
	IsError    bool   `json:"isError,omitempty"`

	Timestamp int64 `json:"timestamp"`
}

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type Context struct {
	SystemPrompt string    `json:"systemPrompt,omitempty"`
	Messages     []Message `json:"messages"`
	Tools        []Tool    `json:"tools,omitempty"`
}

type OpenAICompletionsCompat struct {
	SupportsStore                    *bool                    `json:"supportsStore,omitempty"`
	SupportsDeveloperRole            *bool                    `json:"supportsDeveloperRole,omitempty"`
	SupportsReasoningEffort          *bool                    `json:"supportsReasoningEffort,omitempty"`
	ReasoningEffortMap               map[ThinkingLevel]string `json:"reasoningEffortMap,omitempty"`
	SupportsUsageInStreaming         *bool                    `json:"supportsUsageInStreaming,omitempty"`
	MaxTokensField                   string                   `json:"maxTokensField,omitempty"` // max_completion_tokens|max_tokens
	RequiresToolResultName           *bool                    `json:"requiresToolResultName,omitempty"`
	RequiresAssistantAfterToolResult *bool                    `json:"requiresAssistantAfterToolResult,omitempty"`
	RequiresThinkingAsText           *bool                    `json:"requiresThinkingAsText,omitempty"`
	RequiresMistralToolIDs           *bool                    `json:"requiresMistralToolIds,omitempty"`
	ThinkingFormat                   string                   `json:"thinkingFormat,omitempty"` // openai|zai|qwen
	SupportsStrictMode               *bool                    `json:"supportsStrictMode,omitempty"`
}

type ModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

type Model struct {
	ID            string                   `json:"id"`
	Name          string                   `json:"name"`
	API           Api                      `json:"api"`
	Provider      Provider                 `json:"provider"`
	BaseURL       string                   `json:"baseUrl"`
	Reasoning     bool                     `json:"reasoning"`
	Input         []string                 `json:"input"` // text,image
	Cost          ModelCost                `json:"cost"`
	ContextWindow int                      `json:"contextWindow"`
	MaxTokens     int                      `json:"maxTokens"`
	Headers       map[string]string        `json:"headers,omitempty"`
	Compat        *OpenAICompletionsCompat `json:"compat,omitempty"`
}

type AssistantMessageEventType string

const (
	EventStart         AssistantMessageEventType = "start"
	EventTextStart     AssistantMessageEventType = "text_start"
	EventTextDelta     AssistantMessageEventType = "text_delta"
	EventTextEnd       AssistantMessageEventType = "text_end"
	EventThinkingStart AssistantMessageEventType = "thinking_start"
	EventThinkingDelta AssistantMessageEventType = "thinking_delta"
	EventThinkingEnd   AssistantMessageEventType = "thinking_end"
	EventToolCallStart AssistantMessageEventType = "toolcall_start"
	EventToolCallDelta AssistantMessageEventType = "toolcall_delta"
	EventToolCallEnd   AssistantMessageEventType = "toolcall_end"
	EventDone          AssistantMessageEventType = "done"
	EventError         AssistantMessageEventType = "error"
)

type AssistantMessageEvent struct {
	Type         AssistantMessageEventType
	ContentIndex int
	Delta        string
	Content      string
	ToolCall     *ContentBlock
	Partial      Message
	Message      Message
	Error        Message
	Reason       StopReason
}
