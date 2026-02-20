package memory

import (
	"context"

	"github.com/openai/openai-go/v3"

	iruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
	memorycore "github.com/beeper/ai-bridge/pkg/memory"
)

type SearchOptions = memorycore.SearchOptions
type SearchResult = memorycore.SearchResult
type FallbackStatus = memorycore.FallbackStatus
type ProviderStatus = memorycore.ProviderStatus
type ResolvedConfig = memorycore.ResolvedConfig

type SourceCount struct {
	Source string
	Files  int
	Chunks int
}

type CacheStatus struct {
	Enabled    bool
	Entries    int
	MaxEntries int
}

type FTSStatus struct {
	Enabled   bool
	Available bool
	Error     string
}

type VectorStatus struct {
	Enabled       bool
	Available     *bool
	ExtensionPath string
	LoadError     string
	Dims          int
}

type BatchStatus struct {
	Enabled        bool
	Failures       int
	Limit          int
	Wait           bool
	Concurrency    int
	PollIntervalMs int
	TimeoutMs      int
	LastError      string
	LastProvider   string
}

type StatusDetails struct {
	Files             int
	Chunks            int
	Dirty             bool
	WorkspaceDir      string
	DBPath            string
	Provider          string
	Model             string
	RequestedProvider string
	Sources           []string
	ExtraPaths        []string
	SourceCounts      []SourceCount
	Cache             *CacheStatus
	FTS               *FTSStatus
	Fallback          *FallbackStatus
	Vector            *VectorStatus
	Batch             *BatchStatus
}

type Manager interface {
	Status() ProviderStatus
	Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error)
	ReadFile(ctx context.Context, relPath string, from, lines *int) (map[string]any, error)
	StatusDetails(ctx context.Context) (*StatusDetails, error)
	ProbeVectorAvailability(ctx context.Context) bool
	ProbeEmbeddingAvailability(ctx context.Context) (bool, string)
	SyncWithProgress(ctx context.Context, onProgress func(completed, total int, label string)) error
}

type Host interface {
	ToolDefinitions(ctx context.Context, scope iruntime.ToolScope) []iruntime.ToolDefinition
	ExecuteTool(ctx context.Context, call iruntime.ToolCall) (handled bool, result string, err error)
	ToolAvailability(ctx context.Context, scope iruntime.ToolScope, toolName string) (known bool, available bool, source iruntime.SettingSource, reason string)

	AdditionalSystemMessages(ctx context.Context, scope iruntime.PromptScope) []openai.ChatCompletionMessageParamUnion
	AugmentPrompt(ctx context.Context, scope iruntime.PromptScope, prompt []openai.ChatCompletionMessageParamUnion) []openai.ChatCompletionMessageParamUnion
	GetManager(scope iruntime.ToolScope) (Manager, string)

	StopForLogin(bridgeID, loginID string)
	PurgeForLogin(ctx context.Context, bridgeID, loginID string, chunkIDsByAgent map[string][]string)
}

type Integration struct {
	host Host
}

func NewIntegration(host Host) *Integration {
	return &Integration{host: host}
}

func (i *Integration) Name() string { return "memory" }

func (i *Integration) ToolDefinitions(ctx context.Context, scope iruntime.ToolScope) []iruntime.ToolDefinition {
	if i == nil || i.host == nil {
		return nil
	}
	return i.host.ToolDefinitions(ctx, scope)
}

func (i *Integration) ExecuteTool(ctx context.Context, call iruntime.ToolCall) (bool, string, error) {
	if i == nil || i.host == nil {
		return false, "", nil
	}
	return i.host.ExecuteTool(ctx, call)
}

func (i *Integration) ToolAvailability(ctx context.Context, scope iruntime.ToolScope, toolName string) (bool, bool, iruntime.SettingSource, string) {
	if i == nil || i.host == nil {
		return false, false, iruntime.SourceGlobalDefault, ""
	}
	return i.host.ToolAvailability(ctx, scope, toolName)
}

func (i *Integration) AdditionalSystemMessages(ctx context.Context, scope iruntime.PromptScope) []openai.ChatCompletionMessageParamUnion {
	if i == nil || i.host == nil {
		return nil
	}
	return i.host.AdditionalSystemMessages(ctx, scope)
}

func (i *Integration) AugmentPrompt(ctx context.Context, scope iruntime.PromptScope, prompt []openai.ChatCompletionMessageParamUnion) []openai.ChatCompletionMessageParamUnion {
	if i == nil || i.host == nil {
		return prompt
	}
	return i.host.AugmentPrompt(ctx, scope, prompt)
}

func (i *Integration) GetManager(scope iruntime.ToolScope) (Manager, string) {
	if i == nil || i.host == nil {
		return nil, "memory search unavailable"
	}
	return i.host.GetManager(scope)
}

func (i *Integration) StopForLogin(bridgeID, loginID string) {
	if i == nil || i.host == nil {
		return
	}
	i.host.StopForLogin(bridgeID, loginID)
}

func (i *Integration) PurgeForLogin(ctx context.Context, bridgeID, loginID string, chunkIDsByAgent map[string][]string) {
	if i == nil || i.host == nil {
		return
	}
	i.host.PurgeForLogin(ctx, bridgeID, loginID, chunkIDsByAgent)
}
