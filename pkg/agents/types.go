// Package agents provides the agent system for AI-powered assistants.
// An agent is a persistent entity defined by system prompt, tools, and a swappable model.
// This follows patterns from pi-agent and clawdbot for agent definition and execution.
package agents

import (
	"encoding/json"

	"github.com/beeper/ai-bridge/pkg/agents/toolpolicy"
)

// AgentDefinition is the persistent agent configuration.
type AgentDefinition struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`

	// Model selection (like pi-agent)
	Model ModelConfig `json:"model"`

	// System prompt (base, sections added dynamically)
	SystemPrompt string     `json:"system_prompt,omitempty"`
	PromptMode   PromptMode `json:"prompt_mode,omitempty"` // full, minimal, none

	// Tool policy (OpenClaw-style)
	Tools *toolpolicy.ToolPolicyConfig `json:"tools,omitempty"`

	// Subagent defaults (OpenClaw-style)
	Subagents *SubagentConfig `json:"subagents,omitempty"`

	// Agent behavior
	Temperature     float64      `json:"temperature,omitempty"`
	ReasoningEffort string       `json:"reasoning_effort,omitempty"` // none, low, medium, high
	ResponseMode    ResponseMode `json:"response_mode,omitempty"`    // natural (OpenClaw-style), raw (pass-through)
	Identity        *Identity    `json:"identity,omitempty"`         // custom identity for prompt
	HeartbeatPrompt string       `json:"heartbeat_prompt,omitempty"` // prompt for heartbeat polling (clawdbot parity)

	// Memory search configuration override (OpenClaw-style)
	MemorySearch *MemorySearchConfig `json:"memory_search,omitempty"`

	// Metadata
	IsPreset  bool  `json:"is_preset,omitempty"`
	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

// ModelConfig supports primary + fallback chain (like pi-agent).
type ModelConfig struct {
	Primary   string   `json:"primary"`             // e.g., "anthropic/claude-sonnet-4.5"
	Fallbacks []string `json:"fallbacks,omitempty"` // fallback chain
}

// PromptMode controls system prompt generation (like pi-agent).
type PromptMode string

const (
	// PromptModeFull includes all sections in the system prompt.
	PromptModeFull PromptMode = "full"
	// PromptModeMinimal includes reduced sections for subagents.
	PromptModeMinimal PromptMode = "minimal"
	// PromptModeNone includes just identity, no additional sections.
	PromptModeNone PromptMode = "none"
)

// ResponseMode controls how LLM output is processed before delivery.
// Matches OpenClaw's behavior patterns.
type ResponseMode string

const (
	// ResponseModeNatural processes directives (reply tags, silent replies).
	// Reactions require the message tool. Matches OpenClaw behavior.
	ResponseModeNatural ResponseMode = "natural"
	// ResponseModeRaw passes LLM output directly to user without processing.
	ResponseModeRaw ResponseMode = "raw"
)

// Identity represents a custom agent persona.
type Identity struct {
	Name    string `json:"name,omitempty"`
	Persona string `json:"persona,omitempty"`
}

// SubagentConfig configures default subagent behavior for an agent.
type SubagentConfig struct {
	Model       string   `json:"model,omitempty"`
	Thinking    string   `json:"thinking,omitempty"`
	AllowAgents []string `json:"allowAgents,omitempty"`
}

// MemorySearchConfig configures semantic memory search (OpenClaw-style).
type MemorySearchConfig struct {
	Enabled      *bool                           `json:"enabled,omitempty"`
	Sources      []string                        `json:"sources,omitempty"`
	ExtraPaths   []string                        `json:"extra_paths,omitempty"`
	Provider     string                          `json:"provider,omitempty"`
	Model        string                          `json:"model,omitempty"`
	Remote       *MemorySearchRemoteConfig       `json:"remote,omitempty"`
	Fallback     string                          `json:"fallback,omitempty"`
	Store        *MemorySearchStoreConfig        `json:"store,omitempty"`
	Chunking     *MemorySearchChunkingConfig     `json:"chunking,omitempty"`
	Sync         *MemorySearchSyncConfig         `json:"sync,omitempty"`
	Query        *MemorySearchQueryConfig        `json:"query,omitempty"`
	Cache        *MemorySearchCacheConfig        `json:"cache,omitempty"`
	Experimental *MemorySearchExperimentalConfig `json:"experimental,omitempty"`
}

type MemorySearchRemoteConfig struct {
	BaseURL string                   `json:"base_url,omitempty"`
	APIKey  string                   `json:"api_key,omitempty"`
	Headers map[string]string        `json:"headers,omitempty"`
	Batch   *MemorySearchBatchConfig `json:"batch,omitempty"`
}

type MemorySearchBatchConfig struct {
	Enabled        *bool `json:"enabled,omitempty"`
	Wait           *bool `json:"wait,omitempty"`
	Concurrency    int   `json:"concurrency,omitempty"`
	PollIntervalMs int   `json:"poll_interval_ms,omitempty"`
	TimeoutMinutes int   `json:"timeout_minutes,omitempty"`
}

type MemorySearchStoreConfig struct {
	Driver string                    `json:"driver,omitempty"`
	Path   string                    `json:"path,omitempty"`
	Vector *MemorySearchVectorConfig `json:"vector,omitempty"`
}

type MemorySearchVectorConfig struct {
	Enabled       *bool  `json:"enabled,omitempty"`
	ExtensionPath string `json:"extension_path,omitempty"`
}

type MemorySearchChunkingConfig struct {
	Tokens  int `json:"tokens,omitempty"`
	Overlap int `json:"overlap,omitempty"`
}

type MemorySearchSyncConfig struct {
	OnSessionStart  *bool                          `json:"on_session_start,omitempty"`
	OnSearch        *bool                          `json:"on_search,omitempty"`
	Watch           *bool                          `json:"watch,omitempty"`
	WatchDebounceMs int                            `json:"watch_debounce_ms,omitempty"`
	IntervalMinutes int                            `json:"interval_minutes,omitempty"`
	Sessions        *MemorySearchSessionSyncConfig `json:"sessions,omitempty"`
}

type MemorySearchSessionSyncConfig struct {
	DeltaBytes    int `json:"delta_bytes,omitempty"`
	DeltaMessages int `json:"delta_messages,omitempty"`
}

type MemorySearchQueryConfig struct {
	MaxResults int                       `json:"max_results,omitempty"`
	MinScore   float64                   `json:"min_score,omitempty"`
	Hybrid     *MemorySearchHybridConfig `json:"hybrid,omitempty"`
}

type MemorySearchHybridConfig struct {
	Enabled             *bool   `json:"enabled,omitempty"`
	VectorWeight        float64 `json:"vector_weight,omitempty"`
	TextWeight          float64 `json:"text_weight,omitempty"`
	CandidateMultiplier int     `json:"candidate_multiplier,omitempty"`
}

type MemorySearchCacheConfig struct {
	Enabled    *bool `json:"enabled,omitempty"`
	MaxEntries int   `json:"max_entries,omitempty"`
}

type MemorySearchExperimentalConfig struct {
	SessionMemory *bool `json:"session_memory,omitempty"`
}

// ModelInfo provides metadata about an available model.
type ModelInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Provider    string `json:"provider,omitempty"`
	Description string `json:"description,omitempty"`

	// Capabilities
	SupportsVision        bool `json:"supports_vision,omitempty"`
	SupportsTools         bool `json:"supports_tools,omitempty"`
	SupportsReasoning     bool `json:"supports_reasoning,omitempty"`
	SupportsStreaming     bool `json:"supports_streaming,omitempty"`
	SupportsCodeExecution bool `json:"supports_code_execution,omitempty"`
	SupportsWebSearch     bool `json:"supports_web_search,omitempty"`

	// Limits
	MaxContextTokens    int `json:"max_context_tokens,omitempty"`
	MaxCompletionTokens int `json:"max_completion_tokens,omitempty"`
}

// Clone creates a deep copy of the agent definition.
func (a *AgentDefinition) Clone() *AgentDefinition {
	if a == nil {
		return nil
	}

	clone := &AgentDefinition{
		ID:              a.ID,
		Name:            a.Name,
		Description:     a.Description,
		AvatarURL:       a.AvatarURL,
		Model:           a.Model.Clone(),
		SystemPrompt:    a.SystemPrompt,
		PromptMode:      a.PromptMode,
		Tools:           a.Tools.Clone(),
		Subagents:       cloneSubagentConfig(a.Subagents),
		Temperature:     a.Temperature,
		ReasoningEffort: a.ReasoningEffort,
		IsPreset:        a.IsPreset,
		CreatedAt:       a.CreatedAt,
		UpdatedAt:       a.UpdatedAt,
	}

	if a.Identity != nil {
		clone.Identity = &Identity{
			Name:    a.Identity.Name,
			Persona: a.Identity.Persona,
		}
	}

	if a.MemorySearch != nil {
		copyBytes, _ := json.Marshal(a.MemorySearch)
		var ms MemorySearchConfig
		if err := json.Unmarshal(copyBytes, &ms); err == nil {
			clone.MemorySearch = &ms
		}
	}

	return clone
}

func cloneSubagentConfig(cfg *SubagentConfig) *SubagentConfig {
	if cfg == nil {
		return nil
	}
	out := &SubagentConfig{
		Model: cfg.Model,
	}
	if len(cfg.AllowAgents) > 0 {
		out.AllowAgents = append([]string{}, cfg.AllowAgents...)
	}
	return out
}

// Clone creates a copy of the model config.
func (m ModelConfig) Clone() ModelConfig {
	clone := ModelConfig{
		Primary: m.Primary,
	}
	if m.Fallbacks != nil {
		clone.Fallbacks = make([]string, len(m.Fallbacks))
		copy(clone.Fallbacks, m.Fallbacks)
	}
	return clone
}

// EffectiveModel returns the primary model, falling back to a default if empty.
func (m ModelConfig) EffectiveModel(defaultModel string) string {
	if m.Primary != "" {
		return m.Primary
	}
	return defaultModel
}

// MarshalJSON implements json.Marshaler.
func (a *AgentDefinition) MarshalJSON() ([]byte, error) {
	type Alias AgentDefinition
	return json.Marshal((*Alias)(a))
}

// UnmarshalJSON implements json.Unmarshaler.
func (a *AgentDefinition) UnmarshalJSON(data []byte) error {
	type Alias AgentDefinition
	aux := (*Alias)(a)
	return json.Unmarshal(data, aux)
}

// Validate checks if the agent definition is valid.
func (a *AgentDefinition) Validate() error {
	if a.ID == "" {
		return ErrMissingAgentID
	}
	if a.Name == "" {
		return ErrMissingAgentName
	}
	return nil
}

// IsCustom returns true if this is a user-created (non-preset) agent.
func (a *AgentDefinition) IsCustom() bool {
	return !a.IsPreset
}

// EffectiveName returns the agent's display name, preferring identity name over the base name.
func (a *AgentDefinition) EffectiveName() string {
	if a.Identity != nil && a.Identity.Name != "" {
		return a.Identity.Name
	}
	return a.Name
}
