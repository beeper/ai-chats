package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/agents"
	iruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
	memorycore "github.com/beeper/agentremote/pkg/memory"
	"github.com/beeper/agentremote/pkg/shared/toolspec"
	"github.com/beeper/agentremote/pkg/textfs"
)

const moduleName = "memory"

type SearchOptions = memorycore.SearchOptions
type SearchResult = memorycore.SearchResult
type FallbackStatus = memorycore.FallbackStatus
type ProviderStatus = memorycore.ProviderStatus
type ResolvedConfig = memorycore.ResolvedConfig

type IntegrationDeps struct {
	StateDB      *dbutil.Database
	BridgeID     string
	LoginID      string
	WorkspaceDir string
}

// Integration is the self-owned memory integration module.
// It implements ToolIntegration, CommandIntegration, EventIntegration,
// LoginPurgeIntegration, and LoginLifecycleIntegration
// directly, wiring all deps from Host
// capability interfaces.
type Integration struct {
	host iruntime.Host
	deps IntegrationDeps
}

func New(host iruntime.Host) iruntime.ModuleHooks {
	return NewWithDeps(host, IntegrationDeps{})
}

func NewWithDeps(host iruntime.Host, deps IntegrationDeps) iruntime.ModuleHooks {
	return iruntime.ModuleOrNil(host, func(host iruntime.Host) *Integration {
		return &Integration{host: host, deps: deps}
	})
}

func (i *Integration) Name() string { return moduleName }

func (i *Integration) ToolDefinitions(_ context.Context, _ iruntime.ToolScope) []iruntime.ToolDefinition {
	return []iruntime.ToolDefinition{
		{
			Name:        toolspec.MemorySearchName,
			Description: toolspec.MemorySearchDescription,
			Parameters:  toolspec.MemorySearchSchema(),
		},
		{
			Name:        toolspec.MemoryGetName,
			Description: toolspec.MemoryGetDescription,
			Parameters:  toolspec.MemoryGetSchema(),
		},
	}
}

func (i *Integration) ExecuteTool(ctx context.Context, call iruntime.ToolCall) (bool, string, error) {
	if !iruntime.MatchesAnyName(call.Name, "memory_search", "memory_get") {
		return false, "", nil
	}
	return ExecuteTool(ctx, call, i.buildToolExecDeps())
}

func (i *Integration) ToolAvailability(_ context.Context, scope iruntime.ToolScope, toolName string) (bool, bool, iruntime.SettingSource, string) {
	if !iruntime.MatchesAnyName(toolName, "memory_search", "memory_get") {
		return false, false, iruntime.SourceGlobalDefault, ""
	}
	if scope.Meta != nil {
		agentID := i.agentIDFromEventMeta(scope.Meta)
		_, errMsg := i.getManager(agentID)
		if errMsg != "" {
			return true, false, iruntime.SourceProviderLimit, errMsg
		}
	}
	return true, true, iruntime.SourceGlobalDefault, ""
}

func (i *Integration) PromptContextText(ctx context.Context, scope iruntime.PromptScope) string {
	return BuildPromptContextText(ctx, scope.Portal, scope.Meta, PromptContextDeps{
		ShouldInjectContext:   i.shouldInjectMemoryPromptContext,
		ShouldBootstrap:       i.shouldBootstrapMemoryPromptContext,
		ResolveBootstrapPaths: i.resolveMemoryBootstrapPaths,
		MarkBootstrapped:      i.markMemoryPromptBootstrapped,
		ReadSection:           i.readMemoryPromptSection,
	})
}

func (i *Integration) CommandDefinitions(_ context.Context, _ iruntime.CommandScope) []iruntime.CommandDefinition {
	return []iruntime.CommandDefinition{{
		Name:           "memory",
		Description:    "Inspect and edit memory files/index",
		Args:           "<status|reindex|search|get|set|append> [...]",
		RequiresPortal: true,
		RequiresLogin:  true,
		AdminOnly:      true,
	}}
}

func (i *Integration) ExecuteCommand(ctx context.Context, call iruntime.CommandCall) (bool, error) {
	if !iruntime.MatchesName(call.Name, moduleName) {
		return false, nil
	}
	return ExecuteCommand(ctx, call, i.buildCommandExecDeps())
}

func (i *Integration) OnSessionMutation(ctx context.Context, evt iruntime.SessionMutationEvent) {
	agentID := i.agentIDFromEventMeta(evt.Meta)
	manager, _ := i.getManager(agentID)
	if manager == nil {
		return
	}
	manager.NotifySessionChanged(ctx, evt.SessionKey, evt.Force)
}

func (i *Integration) OnFileChanged(_ context.Context, evt iruntime.FileChangedEvent) {
	agentID := i.agentIDFromEventMeta(evt.Meta)
	manager, _ := i.getManager(agentID)
	if manager == nil {
		return
	}
	manager.NotifyFileChanged(evt.Path)
}

func (i *Integration) OnContextOverflow(ctx context.Context, call iruntime.ContextOverflowCall) {
	HandleOverflow(ctx, call, call.Prompt, i.buildOverflowDeps())
}

func (i *Integration) OnCompactionLifecycle(ctx context.Context, evt iruntime.CompactionLifecycleEvent) {
	if evt.Meta == nil {
		return
	}
	state := evt.Meta.EnsureMemoryState()
	if state == nil {
		return
	}
	state.ApplyCompactionLifecycle(evt.Phase, evt.DroppedCount, evt.Error, time.Now())
	if evt.Portal == nil {
		return
	}
	if err := i.host.SavePortal(ctx, evt.Portal, "compaction lifecycle"); err != nil {
		i.host.Logger().Warn("failed to persist compaction lifecycle metadata", map[string]any{
			"error": err.Error(),
			"phase": string(evt.Phase),
		})
	}
}

func (i *Integration) StopForLogin(bridgeID, loginID string) {
	StopManagersForLogin(bridgeID, loginID)
}

func (i *Integration) PurgeForLogin(ctx context.Context, scope iruntime.LoginScope) error {
	StopManagersForLogin(scope.BridgeID, scope.LoginID)
	db := i.deps.StateDB
	if db == nil {
		return nil
	}
	return PurgeTables(ctx, db, scope.BridgeID, scope.LoginID)
}

func (i *Integration) managerForScope(scope iruntime.ToolScope) (execManager, string) {
	agentID := i.agentIDFromEventMeta(scope.Meta)
	return i.getManager(agentID)
}

func (i *Integration) sessionKeyForScope(scope iruntime.ToolScope) string {
	if scope.Portal == nil {
		return ""
	}
	return scope.Portal.PortalKey.String()
}

func (i *Integration) buildToolExecDeps() ToolExecDeps {
	return ToolExecDeps{
		GetManager:             i.managerForScope,
		ResolveSessionKey:      i.sessionKeyForScope,
		ResolveCitationsMode:   func(_ iruntime.ToolScope) string { return i.resolveMemoryCitationsMode() },
		ShouldIncludeCitations: i.shouldIncludeMemoryCitations,
	}
}

func (i *Integration) buildCommandExecDeps() CommandExecDeps {
	return CommandExecDeps{
		GetManager:        i.managerForScope,
		ResolveSessionKey: i.sessionKeyForScope,
		SplitQuotedArgs:   splitQuotedArgs,
		WriteFile:         i.writeMemoryCommandFile,
	}
}

func (i *Integration) buildOverflowDeps() OverflowDeps {
	return OverflowDeps{
		ResolveSettings: i.resolveOverflowFlushSettings,
		TrimPrompt: func(prompt []openai.ChatCompletionMessageParamUnion) []openai.ChatCompletionMessageParamUnion {
			return i.host.SmartTruncatePrompt(prompt, 0.5)
		},
		ContextWindow: func(call iruntime.ContextOverflowCall) int {
			return i.host.ContextWindow(call.Meta)
		},
		ReserveTokens: func() int {
			return i.host.CompactorReserveTokens()
		},
		EffectiveModel: func(call iruntime.ContextOverflowCall) string {
			return i.host.EffectiveModel(call.Meta)
		},
		EstimateTokens: func(prompt []openai.ChatCompletionMessageParamUnion, model string) int {
			return i.host.EstimateTokens(prompt, model)
		},
		AlreadyFlushed: func(call iruntime.ContextOverflowCall) bool {
			if call.Meta == nil {
				return false
			}
			return call.Meta.MemoryState().AlreadyFlushed(call.Meta.CompactionCounter())
		},
		MarkFlushed: func(ctx context.Context, call iruntime.ContextOverflowCall) {
			if call.Portal == nil || call.Meta == nil {
				return
			}
			state := call.Meta.EnsureMemoryState()
			if state == nil {
				return
			}
			state.MarkOverflowFlushed(call.Meta.CompactionCounter(), time.Now())
			_ = i.host.SavePortal(ctx, call.Portal, "overflow flush")
		},
		RunFlushToolLoop: func(ctx context.Context, call iruntime.ContextOverflowCall, model string, prompt []openai.ChatCompletionMessageParamUnion) (bool, error) {
			return i.runFlushToolLoop(ctx, call.Portal, call.Meta, model, prompt)
		},
		OnError: func(_ context.Context, err error) {
			i.host.Logger().Warn("overflow flush failed", map[string]any{"error": err.Error()})
		},
	}
}

func (i *Integration) shouldInjectMemoryPromptContext(_ *bridgev2.Portal, _ iruntime.Meta) bool {
	if cfg := i.host.ModuleConfig(moduleName); cfg != nil {
		inject, _ := cfg["inject_context"].(bool)
		return inject
	}
	return false
}

func (i *Integration) shouldBootstrapMemoryPromptContext(_ *bridgev2.Portal, meta iruntime.Meta) bool {
	if meta == nil {
		return false
	}
	return meta.MemoryState().NeedsBootstrap()
}

func (i *Integration) resolveMemoryBootstrapPaths(_ *bridgev2.Portal, _ iruntime.Meta) []string {
	_, loc := i.host.UserTimezone()
	if loc == nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	return []string{
		fmt.Sprintf("memory/%s.md", today),
		fmt.Sprintf("memory/%s.md", yesterday),
	}
}

func (i *Integration) markMemoryPromptBootstrapped(ctx context.Context, portal *bridgev2.Portal, meta iruntime.Meta) {
	if portal == nil || meta == nil {
		return
	}
	state := meta.EnsureMemoryState()
	if state == nil {
		return
	}
	state.MarkBootstrapped(time.Now())
	_ = i.host.SavePortal(ctx, portal, "memory bootstrap")
}

func (i *Integration) readMemoryPromptSection(ctx context.Context, meta iruntime.Meta, path string) string {
	agentID := i.agentIDFromEventMeta(meta)
	content, filePath, found, err := i.host.ReadTextFile(ctx, agentID, path)
	if err != nil || !found {
		return ""
	}
	content = normalizeNewlines(content)
	trunc := textfs.TruncateHead(content, textfs.DefaultMaxLines, textfs.DefaultMaxBytes)
	if trunc.FirstLineExceedsLimit {
		return ""
	}
	text := trunc.Content
	if strings.TrimSpace(text) == "" {
		return ""
	}
	if trunc.Truncated {
		text += "\n\n[truncated]"
	}
	heading := filePath
	if strings.TrimSpace(heading) == "" {
		heading = path
	}
	return fmt.Sprintf("## %s\n%s", heading, text)
}

func (i *Integration) getManager(agentID string) (*MemorySearchManager, string) {
	manager, errMsg := GetMemorySearchManager(i.host, i.deps, agentID)
	if manager == nil {
		if errMsg == "" {
			errMsg = "memory search unavailable"
		}
		return nil, errMsg
	}
	return manager, ""
}

func (i *Integration) runFlushToolLoop(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta iruntime.Meta,
	model string,
	messages []openai.ChatCompletionMessageParamUnion,
) (bool, error) {
	allTools := i.host.AllToolDefinitions()
	var flushTools []iruntime.ToolDefinition
	for _, tool := range allTools {
		if isAllowedFlushTool(tool.Name) {
			flushTools = append(flushTools, tool)
		}
	}
	if len(flushTools) == 0 {
		return false, nil
	}
	toolParams := i.host.ToolsToOpenAIParams(flushTools)

	if err := RunFlushToolLoop(ctx, model, messages, FlushToolLoopDeps{
		TimeoutMs: int64((2 * time.Minute) / time.Millisecond),
		MaxTurns:  6,
		NextTurn: func(ctx context.Context, model string, messages []openai.ChatCompletionMessageParamUnion) (
			openai.ChatCompletionMessageParamUnion,
			[]ModelToolCall,
			bool,
			error,
		) {
			result, err := i.host.NewCompletion(ctx, model, messages, toolParams)
			if err != nil {
				return openai.ChatCompletionMessageParamUnion{}, nil, false, err
			}
			if result == nil || result.Done {
				return openai.ChatCompletionMessageParamUnion{}, nil, true, nil
			}
			calls := make([]ModelToolCall, 0, len(result.ToolCalls))
			for _, tc := range result.ToolCalls {
				calls = append(calls, ModelToolCall{
					ID:       tc.ID,
					Name:     strings.TrimSpace(tc.Name),
					ArgsJSON: tc.ArgsJSON,
				})
			}
			return result.AssistantMessage, calls, len(calls) == 0, nil
		},
		ExecuteTool: func(ctx context.Context, name string, argsJSON string) (string, error) {
			if !i.host.IsToolEnabled(meta, name) {
				return "", fmt.Errorf("tool %s is disabled", name)
			}
			return i.host.ExecuteToolInContext(ctx, portal, meta, name, argsJSON)
		},
		OnToolError: func(name string, err error) {
			i.host.Logger().Warn("overflow flush tool failed", map[string]any{"tool": name, "error": err.Error()})
		},
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (i *Integration) resolveOverflowFlushSettings() *FlushSettings {
	enabled, softThresholdTokens, prompt, systemPrompt := i.host.OverflowFlushConfig()
	silentToken := i.host.SilentReplyToken()
	defaultPrompt, defaultSystemPrompt := defaultFlushPrompts(silentToken)
	return normalizeFlushSettings(
		enabled,
		softThresholdTokens,
		prompt,
		systemPrompt,
		defaultPrompt,
		defaultSystemPrompt,
		silentToken,
	)
}

func (i *Integration) resolveMemoryCitationsMode() string {
	if cfg := i.host.ModuleConfig(moduleName); cfg != nil {
		raw, _ := cfg["citations"].(string)
		return normalizeCitationsMode(raw)
	}
	return "auto"
}

func (i *Integration) shouldIncludeMemoryCitations(ctx context.Context, scope iruntime.ToolScope, mode string) bool {
	switch mode {
	case "on":
		return true
	case "off":
		return false
	}
	if scope.Portal == nil {
		return true
	}
	return !i.host.IsGroupChat(ctx, scope.Portal)
}

func (i *Integration) writeMemoryCommandFile(
	ctx context.Context,
	scope iruntime.CommandScope,
	mode string,
	path string,
	content string,
	maxBytes int,
) (string, error) {
	agentID := i.agentIDFromEventMeta(scope.Meta)
	return i.host.WriteTextFile(ctx, scope.Portal, scope.Meta, agentID, mode, path, content, maxBytes)
}

func (i *Integration) agentIDFromEventMeta(meta iruntime.Meta) string {
	var rawAgentID string
	if meta != nil {
		rawAgentID = meta.AgentID()
	}
	return i.host.ResolveAgentID(rawAgentID)
}

// splitQuotedArgs parses a raw argument string into tokens, respecting quoted segments.
func splitQuotedArgs(input string) ([]string, error) {
	var args []string
	var current strings.Builder
	var quote rune
	for _, r := range input {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t':
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unclosed quote")
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args, nil
}

func resolveMemorySearchConfigFromMaps(defaults map[string]any, agentOverrides map[string]any) (*ResolvedConfig, error) {
	var defaultsCfg *agents.MemorySearchConfig
	if len(defaults) > 0 {
		cfg, err := mapToMemorySearchConfig(defaults)
		if err == nil {
			defaultsCfg = cfg
		}
	}
	var overridesCfg *agents.MemorySearchConfig
	if len(agentOverrides) > 0 {
		cfg, err := mapToMemorySearchConfig(agentOverrides)
		if err == nil {
			overridesCfg = cfg
		}
	}
	resolved := MergeSearchConfig(defaultsCfg, overridesCfg)
	if resolved == nil {
		return nil, fmt.Errorf("memory search disabled")
	}
	return resolved, nil
}

func mapToMemorySearchConfig(m map[string]any) (*agents.MemorySearchConfig, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var out agents.MemorySearchConfig
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
