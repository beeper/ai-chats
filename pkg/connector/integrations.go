package connector

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"

	integrationcron "github.com/beeper/ai-bridge/pkg/integrations/cron"
	integrationmemory "github.com/beeper/ai-bridge/pkg/integrations/memory"
	integrationruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
)

const (
	integrationToolSchedulerName    = "cron"
	integrationToolRecallSearchName = "memory_search"
	integrationToolRecallGetName    = "memory_get"
	legacyRecallRootPath            = "memory/"
	legacyRecallFilePath            = "memory.md"

	integrationModuleScheduler = "cron"
	integrationModuleRecall    = "memory"
)

type toolIntegrationRegistry struct {
	items []integrationruntime.ToolIntegration
}

func (r *toolIntegrationRegistry) register(integration integrationruntime.ToolIntegration) {
	if integration == nil {
		return
	}
	r.items = append(r.items, integration)
}

func (r *toolIntegrationRegistry) definitions(ctx context.Context, scope integrationruntime.ToolScope) []ToolDefinition {
	if r == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var out []ToolDefinition
	for _, integration := range r.items {
		for _, def := range integration.ToolDefinitions(ctx, scope) {
			if strings.TrimSpace(def.Name) == "" {
				continue
			}
			if _, ok := seen[def.Name]; ok {
				continue
			}
			seen[def.Name] = struct{}{}
			out = append(out, def)
		}
	}
	return out
}

func (r *toolIntegrationRegistry) execute(ctx context.Context, call integrationruntime.ToolCall) (bool, string, error) {
	if r == nil {
		return false, "", nil
	}
	for _, integration := range r.items {
		handled, result, err := integration.ExecuteTool(ctx, call)
		if handled {
			return true, result, err
		}
	}
	return false, "", nil
}

func (r *toolIntegrationRegistry) availability(
	ctx context.Context,
	scope integrationruntime.ToolScope,
	toolName string,
) (bool, bool, SettingSource, string) {
	if r == nil {
		return false, false, SourceGlobalDefault, ""
	}
	for _, integration := range r.items {
		known, available, source, reason := integration.ToolAvailability(ctx, scope, toolName)
		if known {
			return true, available, settingSourceFromIntegration(source), reason
		}
	}
	return false, false, SourceGlobalDefault, ""
}

type promptIntegrationRegistry struct {
	items []integrationruntime.PromptIntegration
}

func (r *promptIntegrationRegistry) register(integration integrationruntime.PromptIntegration) {
	if integration == nil {
		return
	}
	r.items = append(r.items, integration)
}

func (r *promptIntegrationRegistry) additionalMessages(
	ctx context.Context,
	scope integrationruntime.PromptScope,
) []openai.ChatCompletionMessageParamUnion {
	if r == nil {
		return nil
	}
	var out []openai.ChatCompletionMessageParamUnion
	for _, integration := range r.items {
		out = append(out, integration.AdditionalSystemMessages(ctx, scope)...)
	}
	return out
}

func (r *promptIntegrationRegistry) augmentPrompt(
	ctx context.Context,
	scope integrationruntime.PromptScope,
	prompt []openai.ChatCompletionMessageParamUnion,
) []openai.ChatCompletionMessageParamUnion {
	if r == nil {
		return prompt
	}
	out := prompt
	for _, integration := range r.items {
		out = integration.AugmentPrompt(ctx, scope, out)
	}
	return out
}

func settingSourceFromIntegration(source integrationruntime.SettingSource) SettingSource {
	switch source {
	case integrationruntime.SourceAgentPolicy:
		return SourceAgentPolicy
	case integrationruntime.SourceRoomOverride:
		return SourceRoomOverride
	case integrationruntime.SourceUserDefault:
		return SourceUserDefault
	case integrationruntime.SourceProviderConfig:
		return SourceProviderConfig
	case integrationruntime.SourceGlobalDefault:
		return SourceGlobalDefault
	case integrationruntime.SourceModelLimit:
		return SourceModelLimit
	case integrationruntime.SourceProviderLimit:
		return SourceProviderLimit
	default:
		return SourceGlobalDefault
	}
}

func settingSourceToIntegration(source SettingSource) integrationruntime.SettingSource {
	switch source {
	case SourceAgentPolicy:
		return integrationruntime.SourceAgentPolicy
	case SourceRoomOverride:
		return integrationruntime.SourceRoomOverride
	case SourceUserDefault:
		return integrationruntime.SourceUserDefault
	case SourceProviderConfig:
		return integrationruntime.SourceProviderConfig
	case SourceGlobalDefault:
		return integrationruntime.SourceGlobalDefault
	case SourceModelLimit:
		return integrationruntime.SourceModelLimit
	case SourceProviderLimit:
		return integrationruntime.SourceProviderLimit
	default:
		return integrationruntime.SourceGlobalDefault
	}
}

func (oc *AIClient) toolScope(portal *bridgev2.Portal, meta *PortalMetadata) integrationruntime.ToolScope {
	return integrationruntime.ToolScope{
		Client: oc,
		Portal: portal,
		Meta:   meta,
	}
}

func (oc *AIClient) promptScope(portal *bridgev2.Portal, meta *PortalMetadata) integrationruntime.PromptScope {
	return integrationruntime.PromptScope{
		Client: oc,
		Portal: portal,
		Meta:   meta,
	}
}

func (oc *AIClient) initIntegrations() {
	if oc == nil {
		return
	}
	oc.toolRegistry = &toolIntegrationRegistry{}
	oc.promptRegistry = &promptIntegrationRegistry{}
	oc.integrationModules = make(map[string]any)
	oc.integrationOrder = nil

	oc.toolRegistry.register(&coreToolIntegration{client: oc})
	oc.promptRegistry.register(&corePromptIntegration{client: oc})

	if oc.schedulerModuleEnabled() {
		cronAdapter := &cronConnectorHostAdapter{client: oc}
		cronAdapter.service = oc.buildCronService()
		module := integrationcron.NewIntegration(cronAdapter)
		oc.registerIntegrationModule(module.Name(), module)
		oc.toolRegistry.register(module)
	}

	if oc.recallModuleEnabled() {
		module := integrationmemory.NewIntegration(&memoryConnectorHostAdapter{client: oc})
		oc.registerIntegrationModule(module.Name(), module)
		oc.toolRegistry.register(module)
		oc.promptRegistry.register(module)
	}
}

func (oc *AIClient) registerIntegrationModule(name string, module any) {
	if oc == nil || module == nil {
		return
	}
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return
	}
	if oc.integrationModules == nil {
		oc.integrationModules = make(map[string]any)
	}
	if _, exists := oc.integrationModules[key]; exists {
		return
	}
	oc.integrationModules[key] = module
	oc.integrationOrder = append(oc.integrationOrder, key)
}

func (oc *AIClient) integrationModule(name string) any {
	if oc == nil || oc.integrationModules == nil {
		return nil
	}
	return oc.integrationModules[strings.ToLower(strings.TrimSpace(name))]
}

func (oc *AIClient) schedulerModule() *integrationcron.Integration {
	if module, ok := oc.integrationModule(integrationModuleScheduler).(*integrationcron.Integration); ok {
		return module
	}
	return nil
}

func (oc *AIClient) recallModule() *integrationmemory.Integration {
	if module, ok := oc.integrationModule(integrationModuleRecall).(*integrationmemory.Integration); ok {
		return module
	}
	return nil
}

func (oc *AIClient) eachIntegrationModule(fn func(name string, module any)) {
	if oc == nil || fn == nil || len(oc.integrationOrder) == 0 {
		return
	}
	for _, name := range oc.integrationOrder {
		module := oc.integrationModule(name)
		if module == nil {
			continue
		}
		fn(name, module)
	}
}

func (oc *AIClient) startLifecycleIntegrations(ctx context.Context) {
	if oc == nil {
		return
	}
	oc.eachIntegrationModule(func(name string, module any) {
		lifecycle, ok := module.(integrationruntime.LifecycleIntegration)
		if !ok {
			return
		}
		if err := lifecycle.Start(ctx); err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Str("integration", name).Msg("integration start failed, scheduling retry")
			go oc.retryIntegrationStart(oc.disconnectCtx, name, lifecycle)
		}
	})
}

func (oc *AIClient) retryIntegrationStart(ctx context.Context, name string, lifecycle integrationruntime.LifecycleIntegration) {
	if oc == nil || lifecycle == nil {
		return
	}
	delays := []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second}
	for _, d := range delays {
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
		current := oc.integrationModule(name)
		if current == nil {
			return
		}
		currentLifecycle, ok := current.(integrationruntime.LifecycleIntegration)
		if !ok {
			return
		}
		if err := currentLifecycle.Start(ctx); err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Str("integration", name).Dur("retryDelay", d).Msg("integration start retry failed")
			continue
		}
		oc.loggerForContext(ctx).Info().Str("integration", name).Msg("integration start retry succeeded")
		return
	}
	oc.loggerForContext(ctx).Error().Str("integration", name).Msg("integration start retries exhausted")
}

func (oc *AIClient) stopLifecycleIntegrations() {
	if oc == nil || len(oc.integrationOrder) == 0 {
		return
	}
	// Stop in reverse registration order.
	for i := len(oc.integrationOrder) - 1; i >= 0; i-- {
		name := oc.integrationOrder[i]
		module := oc.integrationModule(name)
		lifecycle, ok := module.(integrationruntime.LifecycleIntegration)
		if !ok {
			continue
		}
		lifecycle.Stop()
	}
}

func (oc *AIClient) stopLoginLifecycleIntegrations(bridgeID, loginID string) {
	if oc == nil || strings.TrimSpace(bridgeID) == "" || strings.TrimSpace(loginID) == "" {
		return
	}
	oc.eachIntegrationModule(func(_ string, module any) {
		loginLifecycle, ok := module.(integrationruntime.LoginLifecycleIntegration)
		if !ok {
			return
		}
		loginLifecycle.StopForLogin(bridgeID, loginID)
	})
}

func (oc *AIClient) schedulerModuleEnabled() bool {
	if oc == nil || oc.connector == nil || oc.connector.Config.Integrations == nil || oc.connector.Config.Integrations.Scheduler == nil {
		return true
	}
	return *oc.connector.Config.Integrations.Scheduler
}

func (oc *AIClient) recallModuleEnabled() bool {
	if oc == nil || oc.connector == nil || oc.connector.Config.Integrations == nil || oc.connector.Config.Integrations.Recall == nil {
		return true
	}
	return *oc.connector.Config.Integrations.Recall
}

func (oc *AIClient) integratedToolDefinitions(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
) []ToolDefinition {
	if oc == nil || oc.toolRegistry == nil {
		return BuiltinTools()
	}
	return oc.toolRegistry.definitions(ctx, oc.toolScope(portal, meta))
}

func (oc *AIClient) integratedToolAvailability(meta *PortalMetadata, toolName string) (bool, bool, SettingSource, string) {
	if oc == nil {
		return false, false, SourceGlobalDefault, ""
	}
	switch strings.TrimSpace(toolName) {
	case ToolNameScheduler:
		if !oc.schedulerModuleEnabled() {
			return true, false, SourceProviderLimit, "Scheduler integration disabled"
		}
		if oc.toolRegistry == nil {
			return true, false, SourceProviderLimit, "Scheduler integration unavailable"
		}
	case ToolNameRecallSearch, ToolNameRecallGet:
		if !oc.recallModuleEnabled() {
			return true, false, SourceProviderLimit, "Recall integration disabled"
		}
		disabled, reason := oc.isRecallSearchExplicitlyDisabled(meta)
		if disabled {
			return true, false, SourceProviderLimit, reason
		}
		if oc.toolRegistry == nil {
			return true, false, SourceProviderLimit, "Recall integration unavailable"
		}
	default:
		if oc.toolRegistry == nil {
			return false, false, SourceGlobalDefault, ""
		}
	}
	if known, available, source, reason := oc.toolRegistry.availability(context.Background(), oc.toolScope(nil, meta), toolName); known {
		return true, available, source, reason
	}
	return false, false, SourceGlobalDefault, ""
}

func (oc *AIClient) executeIntegratedTool(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	toolName string,
	args map[string]any,
	argsJSON string,
) (bool, string, error) {
	if oc == nil || oc.toolRegistry == nil {
		return false, "", nil
	}
	raw := strings.TrimSpace(argsJSON)
	if raw == "" && args != nil {
		blob, err := json.Marshal(args)
		if err == nil {
			raw = string(blob)
		}
	}
	return oc.toolRegistry.execute(ctx, integrationruntime.ToolCall{
		Name:        toolName,
		Args:        args,
		RawArgsJSON: raw,
		Scope:       oc.toolScope(portal, meta),
	})
}

func (oc *AIClient) additionalSystemMessages(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
) []openai.ChatCompletionMessageParamUnion {
	if oc == nil {
		return nil
	}
	if oc.promptRegistry == nil {
		return oc.buildAdditionalSystemPromptsCore(ctx, portal, meta)
	}
	return oc.promptRegistry.additionalMessages(ctx, oc.promptScope(portal, meta))
}

func (oc *AIClient) augmentPromptWithIntegrations(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt []openai.ChatCompletionMessageParamUnion,
) []openai.ChatCompletionMessageParamUnion {
	if oc == nil {
		return prompt
	}
	if oc.promptRegistry == nil {
		return oc.injectMemoryContext(ctx, portal, meta, prompt)
	}
	return oc.promptRegistry.augmentPrompt(ctx, oc.promptScope(portal, meta), prompt)
}

func integrationToolByName(name string) (ToolDefinition, bool) {
	for _, def := range BuiltinTools() {
		if def.Name == name {
			return def, true
		}
	}
	return ToolDefinition{}, false
}

func integrationPortalRoomType(meta *PortalMetadata) string {
	if meta != nil && meta.IsSchedulerRoom {
		return "scheduler"
	}
	return "ai"
}

func isIntegrationSessionKindAllowed(kind string) bool {
	switch kind {
	case "main", "group", "scheduler", "hook", "node", "other":
		return true
	default:
		return false
	}
}

func integrationSessionKind(currentRoomID string, portalRoomID string, meta *PortalMetadata) string {
	if currentRoomID != "" && portalRoomID != "" && portalRoomID == currentRoomID {
		return "main"
	}
	if meta != nil {
		if meta.IsSchedulerRoom {
			return "scheduler"
		}
		if strings.TrimSpace(meta.SubagentParentRoomID) != "" {
			return "other"
		}
		if meta.IsBuilderRoom {
			return "other"
		}
	}
	return "group"
}

type coreToolIntegration struct {
	client *AIClient
}

func (c *coreToolIntegration) Name() string { return "core" }

func (c *coreToolIntegration) ToolDefinitions(_ context.Context, _ integrationruntime.ToolScope) []integrationruntime.ToolDefinition {
	var out []integrationruntime.ToolDefinition
	for _, def := range BuiltinTools() {
		if def.Name == ToolNameScheduler || def.Name == ToolNameRecallSearch || def.Name == ToolNameRecallGet {
			continue
		}
		out = append(out, def)
	}
	return out
}

func (c *coreToolIntegration) ExecuteTool(ctx context.Context, call integrationruntime.ToolCall) (bool, string, error) {
	if c == nil || c.client == nil {
		return false, "", nil
	}
	if call.Name == ToolNameScheduler || call.Name == ToolNameRecallSearch || call.Name == ToolNameRecallGet {
		return false, "", nil
	}
	portal, _ := call.Scope.Portal.(*bridgev2.Portal)
	result, err := c.client.executeBuiltinToolDirect(ctx, portal, call.Name, call.RawArgsJSON)
	if err != nil {
		return true, "", err
	}
	return true, result, nil
}

func (c *coreToolIntegration) ToolAvailability(
	_ context.Context,
	_ integrationruntime.ToolScope,
	_ string,
) (bool, bool, integrationruntime.SettingSource, string) {
	return false, false, integrationruntime.SourceGlobalDefault, ""
}

type corePromptIntegration struct {
	client *AIClient
}

func (c *corePromptIntegration) Name() string { return "core" }

func (c *corePromptIntegration) AdditionalSystemMessages(
	ctx context.Context,
	scope integrationruntime.PromptScope,
) []openai.ChatCompletionMessageParamUnion {
	if c == nil || c.client == nil {
		return nil
	}
	portal, _ := scope.Portal.(*bridgev2.Portal)
	meta, _ := scope.Meta.(*PortalMetadata)
	return c.client.buildAdditionalSystemPromptsCore(ctx, portal, meta)
}

func (c *corePromptIntegration) AugmentPrompt(
	_ context.Context,
	_ integrationruntime.PromptScope,
	prompt []openai.ChatCompletionMessageParamUnion,
) []openai.ChatCompletionMessageParamUnion {
	return prompt
}

type cronConnectorHostAdapter struct {
	client  *AIClient
	service *integrationcron.Service
}

func (a *cronConnectorHostAdapter) ToolDefinitions(_ context.Context, _ integrationruntime.ToolScope) []integrationruntime.ToolDefinition {
	def, ok := integrationToolByName(ToolNameScheduler)
	if !ok {
		return nil
	}
	return []integrationruntime.ToolDefinition{def}
}

func (a *cronConnectorHostAdapter) ExecuteTool(ctx context.Context, call integrationruntime.ToolCall) (bool, string, error) {
	if call.Name != ToolNameScheduler {
		return false, "", nil
	}
	result, err := executeCron(ctx, call.Args)
	return true, result, err
}

func (a *cronConnectorHostAdapter) ToolAvailability(
	_ context.Context,
	_ integrationruntime.ToolScope,
	toolName string,
) (bool, bool, integrationruntime.SettingSource, string) {
	if toolName != ToolNameScheduler {
		return false, false, integrationruntime.SourceGlobalDefault, ""
	}
	if a == nil || a.client == nil {
		return true, false, integrationruntime.SourceProviderLimit, "Cron service not available"
	}
	ok, reason := a.client.isSchedulerConfigured()
	if ok {
		return true, true, integrationruntime.SourceGlobalDefault, ""
	}
	return true, false, integrationruntime.SourceProviderLimit, reason
}

func (a *cronConnectorHostAdapter) Start(_ context.Context) error {
	if a == nil || a.client == nil || a.service == nil {
		return nil
	}
	return a.service.Start()
}

func (a *cronConnectorHostAdapter) Stop() {
	if a == nil || a.client == nil || a.service == nil {
		return
	}
	a.service.Stop()
}

func (a *cronConnectorHostAdapter) Status() (bool, string, int, *int64, error) {
	if a == nil || a.client == nil || a.service == nil {
		return false, "", 0, nil, errors.New("cron service not available")
	}
	return a.service.Status()
}

func (a *cronConnectorHostAdapter) List(includeDisabled bool) ([]integrationcron.Job, error) {
	if a == nil || a.client == nil || a.service == nil {
		return nil, errors.New("cron service not available")
	}
	return a.service.List(includeDisabled)
}

func (a *cronConnectorHostAdapter) Add(input integrationcron.JobCreate) (integrationcron.Job, error) {
	if a == nil || a.client == nil || a.service == nil {
		return integrationcron.Job{}, errors.New("cron service not available")
	}
	return a.service.Add(input)
}

func (a *cronConnectorHostAdapter) Update(id string, patch integrationcron.JobPatch) (integrationcron.Job, error) {
	if a == nil || a.client == nil || a.service == nil {
		return integrationcron.Job{}, errors.New("cron service not available")
	}
	return a.service.Update(id, patch)
}

func (a *cronConnectorHostAdapter) Remove(id string) (bool, error) {
	if a == nil || a.client == nil || a.service == nil {
		return false, errors.New("cron service not available")
	}
	return a.service.Remove(id)
}

func (a *cronConnectorHostAdapter) Run(id string, mode string) (bool, string, error) {
	if a == nil || a.client == nil || a.service == nil {
		return false, "", errors.New("cron service not available")
	}
	return a.service.Run(id, mode)
}

func (a *cronConnectorHostAdapter) Wake(mode string, text string) (bool, error) {
	if a == nil || a.client == nil || a.service == nil {
		return false, errors.New("cron service not available")
	}
	return a.service.Wake(mode, text)
}

func (a *cronConnectorHostAdapter) Runs(jobID string, limit int) ([]integrationcron.RunLogEntry, error) {
	if a == nil || a.client == nil {
		return nil, errors.New("cron service not available")
	}
	return a.client.readCronRuns(jobID, limit)
}

type memoryConnectorHostAdapter struct {
	client *AIClient
}

func (a *memoryConnectorHostAdapter) ToolDefinitions(_ context.Context, _ integrationruntime.ToolScope) []integrationruntime.ToolDefinition {
	var out []integrationruntime.ToolDefinition
	if def, ok := integrationToolByName(ToolNameRecallSearch); ok {
		out = append(out, def)
	}
	if def, ok := integrationToolByName(ToolNameRecallGet); ok {
		out = append(out, def)
	}
	return out
}

func (a *memoryConnectorHostAdapter) ExecuteTool(ctx context.Context, call integrationruntime.ToolCall) (bool, string, error) {
	switch call.Name {
	case ToolNameRecallSearch:
		result, err := executeRecallSearch(ctx, call.Args)
		return true, result, err
	case ToolNameRecallGet:
		result, err := executeRecallGet(ctx, call.Args)
		return true, result, err
	default:
		return false, "", nil
	}
}

func (a *memoryConnectorHostAdapter) ToolAvailability(
	_ context.Context,
	scope integrationruntime.ToolScope,
	toolName string,
) (bool, bool, integrationruntime.SettingSource, string) {
	if toolName != ToolNameRecallSearch && toolName != ToolNameRecallGet {
		return false, false, integrationruntime.SourceGlobalDefault, ""
	}
	if a == nil || a.client == nil {
		return true, false, integrationruntime.SourceProviderLimit, "Memory search unavailable"
	}
	meta, _ := scope.Meta.(*PortalMetadata)
	disabled, reason := a.client.isRecallSearchExplicitlyDisabled(meta)
	if disabled {
		return true, false, integrationruntime.SourceProviderLimit, reason
	}
	return true, true, integrationruntime.SourceGlobalDefault, ""
}

func (a *memoryConnectorHostAdapter) AdditionalSystemMessages(
	_ context.Context,
	_ integrationruntime.PromptScope,
) []openai.ChatCompletionMessageParamUnion {
	return nil
}

func (a *memoryConnectorHostAdapter) AugmentPrompt(
	ctx context.Context,
	scope integrationruntime.PromptScope,
	prompt []openai.ChatCompletionMessageParamUnion,
) []openai.ChatCompletionMessageParamUnion {
	if a == nil || a.client == nil {
		return prompt
	}
	portal, _ := scope.Portal.(*bridgev2.Portal)
	meta, _ := scope.Meta.(*PortalMetadata)
	return a.client.injectMemoryContext(ctx, portal, meta, prompt)
}

func (a *memoryConnectorHostAdapter) GetManager(scope integrationruntime.ToolScope) (integrationmemory.Manager, string) {
	if a == nil || a.client == nil {
		return nil, "memory search unavailable"
	}
	meta, _ := scope.Meta.(*PortalMetadata)
	manager, errMsg := a.client.getRecallManager(resolveAgentID(meta))
	if manager == nil {
		return nil, errMsg
	}
	return &memoryManagerAdapter{manager: manager}, ""
}

func (a *memoryConnectorHostAdapter) StopForLogin(bridgeID, loginID string) {
	integrationmemory.StopManagersForLogin(bridgeID, loginID)
}

func (a *memoryConnectorHostAdapter) PurgeForLogin(ctx context.Context, bridgeID, loginID string, chunkIDsByAgent map[string][]string) {
	integrationmemory.PurgeManagersForLogin(ctx, bridgeID, loginID, chunkIDsByAgent)
}

type memoryManagerAdapter struct {
	manager *integrationmemory.MemorySearchManager
}

func (m *memoryManagerAdapter) Status() integrationmemory.ProviderStatus {
	if m == nil || m.manager == nil {
		return integrationmemory.ProviderStatus{}
	}
	return m.manager.Status()
}

func (m *memoryManagerAdapter) Search(ctx context.Context, query string, opts integrationmemory.SearchOptions) ([]integrationmemory.SearchResult, error) {
	if m == nil || m.manager == nil {
		return nil, errors.New("memory search unavailable")
	}
	return m.manager.Search(ctx, query, opts)
}

func (m *memoryManagerAdapter) ReadFile(ctx context.Context, relPath string, from, lines *int) (map[string]any, error) {
	if m == nil || m.manager == nil {
		return nil, errors.New("memory search unavailable")
	}
	return m.manager.ReadFile(ctx, relPath, from, lines)
}

func (m *memoryManagerAdapter) StatusDetails(ctx context.Context) (*integrationmemory.StatusDetails, error) {
	if m == nil || m.manager == nil {
		return nil, errors.New("memory search unavailable")
	}
	status, err := m.manager.StatusDetails(ctx)
	if err != nil {
		return nil, err
	}
	var sourceCounts []integrationmemory.SourceCount
	if len(status.SourceCounts) > 0 {
		sourceCounts = make([]integrationmemory.SourceCount, 0, len(status.SourceCounts))
		for _, src := range status.SourceCounts {
			sourceCounts = append(sourceCounts, integrationmemory.SourceCount{
				Source: src.Source,
				Files:  src.Files,
				Chunks: src.Chunks,
			})
		}
	}
	var fallback *integrationmemory.FallbackStatus
	if status.Fallback != nil {
		fallback = &integrationmemory.FallbackStatus{
			From:   status.Fallback.From,
			Reason: status.Fallback.Reason,
		}
	}
	var cache *integrationmemory.CacheStatus
	if status.Cache != nil {
		cache = &integrationmemory.CacheStatus{
			Enabled:    status.Cache.Enabled,
			Entries:    status.Cache.Entries,
			MaxEntries: status.Cache.MaxEntries,
		}
	}
	var fts *integrationmemory.FTSStatus
	if status.FTS != nil {
		fts = &integrationmemory.FTSStatus{
			Enabled:   status.FTS.Enabled,
			Available: status.FTS.Available,
			Error:     status.FTS.Error,
		}
	}
	var vector *integrationmemory.VectorStatus
	if status.Vector != nil {
		vector = &integrationmemory.VectorStatus{
			Enabled:       status.Vector.Enabled,
			Available:     status.Vector.Available,
			ExtensionPath: status.Vector.ExtensionPath,
			LoadError:     status.Vector.LoadError,
			Dims:          status.Vector.Dims,
		}
	}
	var batch *integrationmemory.BatchStatus
	if status.Batch != nil {
		batch = &integrationmemory.BatchStatus{
			Enabled:        status.Batch.Enabled,
			Failures:       status.Batch.Failures,
			Limit:          status.Batch.Limit,
			Wait:           status.Batch.Wait,
			Concurrency:    status.Batch.Concurrency,
			PollIntervalMs: status.Batch.PollIntervalMs,
			TimeoutMs:      status.Batch.TimeoutMs,
			LastError:      status.Batch.LastError,
			LastProvider:   status.Batch.LastProvider,
		}
	}
	return &integrationmemory.StatusDetails{
		Files:             status.Files,
		Chunks:            status.Chunks,
		Dirty:             status.Dirty,
		WorkspaceDir:      status.WorkspaceDir,
		DBPath:            status.DBPath,
		Provider:          status.Provider,
		Model:             status.Model,
		RequestedProvider: status.RequestedProvider,
		Sources:           status.Sources,
		ExtraPaths:        status.ExtraPaths,
		SourceCounts:      sourceCounts,
		Cache:             cache,
		FTS:               fts,
		Fallback:          fallback,
		Vector:            vector,
		Batch:             batch,
	}, nil
}

func (m *memoryManagerAdapter) ProbeVectorAvailability(ctx context.Context) bool {
	if m == nil || m.manager == nil {
		return false
	}
	return m.manager.ProbeVectorAvailability(ctx)
}

func (m *memoryManagerAdapter) ProbeEmbeddingAvailability(ctx context.Context) (bool, string) {
	if m == nil || m.manager == nil {
		return false, "memory search unavailable"
	}
	return m.manager.ProbeEmbeddingAvailability(ctx)
}

func (m *memoryManagerAdapter) SyncWithProgress(ctx context.Context, onProgress func(completed, total int, label string)) error {
	if m == nil || m.manager == nil {
		return errors.New("memory search unavailable")
	}
	return m.manager.SyncWithProgress(ctx, onProgress)
}
