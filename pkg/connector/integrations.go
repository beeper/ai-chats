package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"

	integrationmodules "github.com/beeper/ai-bridge/pkg/integrations/modules"
	integrationruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
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
			normalized := strings.ToLower(strings.TrimSpace(def.Name))
			if normalized == "" {
				continue
			}
			if _, ok := seen[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
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

type commandIntegrationRegistration struct {
	integration integrationruntime.CommandIntegration
	definition  integrationruntime.CommandDefinition
}

type commandIntegrationRegistry struct {
	byName map[string]commandIntegrationRegistration
}

func newCommandIntegrationRegistry() *commandIntegrationRegistry {
	return &commandIntegrationRegistry{byName: make(map[string]commandIntegrationRegistration)}
}

func (r *commandIntegrationRegistry) register(integration integrationruntime.CommandIntegration, defs []integrationruntime.CommandDefinition) {
	if r == nil || integration == nil {
		return
	}
	for _, def := range defs {
		name := strings.ToLower(strings.TrimSpace(def.Name))
		if name == "" {
			continue
		}
		if _, exists := r.byName[name]; exists {
			continue
		}
		r.byName[name] = commandIntegrationRegistration{integration: integration, definition: def}
	}
}

func (r *commandIntegrationRegistry) definitions() []integrationruntime.CommandDefinition {
	if r == nil || len(r.byName) == 0 {
		return nil
	}
	names := make([]string, 0, len(r.byName))
	for name := range r.byName {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]integrationruntime.CommandDefinition, 0, len(names))
	for _, name := range names {
		out = append(out, r.byName[name].definition)
	}
	return out
}

func (r *commandIntegrationRegistry) execute(ctx context.Context, call integrationruntime.CommandCall) (bool, error) {
	if r == nil {
		return false, nil
	}
	name := strings.ToLower(strings.TrimSpace(call.Name))
	registration, ok := r.byName[name]
	if !ok || registration.integration == nil {
		return false, nil
	}
	return registration.integration.ExecuteCommand(ctx, call)
}

type eventIntegrationRegistry struct {
	items []integrationruntime.EventIntegration
}

func (r *eventIntegrationRegistry) register(integration integrationruntime.EventIntegration) {
	if integration == nil {
		return
	}
	r.items = append(r.items, integration)
}

func (r *eventIntegrationRegistry) sessionMutation(ctx context.Context, evt integrationruntime.SessionMutationEvent) {
	if r == nil {
		return
	}
	for _, integration := range r.items {
		integration.OnSessionMutation(ctx, evt)
	}
}

func (r *eventIntegrationRegistry) fileChanged(ctx context.Context, evt integrationruntime.FileChangedEvent) {
	if r == nil {
		return
	}
	for _, integration := range r.items {
		integration.OnFileChanged(ctx, evt)
	}
}

type purgeIntegrationRegistry struct {
	items []integrationruntime.LoginPurgeIntegration
}

func (r *purgeIntegrationRegistry) register(integration integrationruntime.LoginPurgeIntegration) {
	if integration == nil {
		return
	}
	r.items = append(r.items, integration)
}

func (r *purgeIntegrationRegistry) purge(ctx context.Context, scope integrationruntime.LoginScope) error {
	if r == nil {
		return nil
	}
	var purgeErrs []error
	for _, integration := range r.items {
		if err := integration.PurgeForLogin(ctx, scope); err != nil {
			purgeErrs = append(purgeErrs, fmt.Errorf("%s: %w", integration.Name(), err))
		}
	}
	return errors.Join(purgeErrs...)
}

type toolApprovalIntegrationRegistry struct {
	items []integrationruntime.ToolApprovalIntegration
}

func (r *toolApprovalIntegrationRegistry) register(integration integrationruntime.ToolApprovalIntegration) {
	if integration == nil {
		return
	}
	r.items = append(r.items, integration)
}

func (r *toolApprovalIntegrationRegistry) requirement(toolName string, args map[string]any) (handled bool, required bool, action string) {
	if r == nil {
		return false, false, ""
	}
	for _, integration := range r.items {
		handled, required, action = integration.ToolApprovalRequirement(toolName, args)
		if handled {
			return handled, required, action
		}
	}
	return false, false, ""
}

func settingSourceFromIntegration(source integrationruntime.SettingSource) SettingSource {
	return SettingSource(source)
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

func (oc *AIClient) commandScope(portal *bridgev2.Portal, meta *PortalMetadata, evt any) integrationruntime.CommandScope {
	return integrationruntime.CommandScope{
		Client: oc,
		Portal: portal,
		Meta:   meta,
		Event:  evt,
	}
}

func (oc *AIClient) initIntegrations() {
	if oc == nil {
		return
	}
	oc.toolRegistry = &toolIntegrationRegistry{}
	oc.promptRegistry = &promptIntegrationRegistry{}
	oc.commandRegistry = newCommandIntegrationRegistry()
	oc.eventRegistry = &eventIntegrationRegistry{}
	oc.purgeRegistry = &purgeIntegrationRegistry{}
	oc.approvalRegistry = &toolApprovalIntegrationRegistry{}
	oc.integrationModules = make(map[string]any)
	oc.integrationOrder = nil

	host := newRuntimeIntegrationHost(oc)
	for _, module := range integrationmodules.BuiltinModules(host) {
		if module == nil {
			continue
		}
		name := module.Name()
		oc.registerIntegrationModule(name, module)

		if toolIntegration, ok := module.(integrationruntime.ToolIntegration); ok {
			oc.toolRegistry.register(toolIntegration)
		}
		if promptIntegration, ok := module.(integrationruntime.PromptIntegration); ok {
			oc.promptRegistry.register(promptIntegration)
		}
		if commandIntegration, ok := module.(integrationruntime.CommandIntegration); ok {
			defs := commandIntegration.CommandDefinitions(context.Background(), oc.commandScope(nil, nil, nil))
			oc.commandRegistry.register(commandIntegration, defs)
		}
		if eventIntegration, ok := module.(integrationruntime.EventIntegration); ok {
			oc.eventRegistry.register(eventIntegration)
		}
		if purgeIntegration, ok := module.(integrationruntime.LoginPurgeIntegration); ok {
			oc.purgeRegistry.register(purgeIntegration)
		}
		if approvalIntegration, ok := module.(integrationruntime.ToolApprovalIntegration); ok {
			oc.approvalRegistry.register(approvalIntegration)
		}
	}

	// Register core integrations after modules so module tool/prompt implementations take precedence.
	coreTools := &coreToolIntegration{client: oc}
	corePrompts := &corePromptIntegration{client: oc}
	oc.toolRegistry.register(coreTools)
	oc.promptRegistry.register(corePrompts)

	registerModuleCommands(oc.commandRegistry.definitions())
}

func (oc *AIClient) integratedToolApprovalRequirement(toolName string, args map[string]any) (handled bool, required bool, action string) {
	if oc == nil || oc.approvalRegistry == nil {
		return false, false, ""
	}
	return oc.approvalRegistry.requirement(toolName, args)
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
	if oc == nil || oc.toolRegistry == nil {
		return false, false, SourceGlobalDefault, ""
	}
	return oc.toolRegistry.availability(context.Background(), oc.toolScope(nil, meta), toolName)
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
		return prompt
	}
	return oc.promptRegistry.augmentPrompt(ctx, oc.promptScope(portal, meta), prompt)
}

func (oc *AIClient) executeIntegratedCommand(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	evt any,
	name string,
	args []string,
	rawArgs string,
	reply func(format string, args ...any),
) (bool, error) {
	if oc == nil || oc.commandRegistry == nil {
		return false, nil
	}
	return oc.commandRegistry.execute(ctx, integrationruntime.CommandCall{
		Name:    name,
		Args:    args,
		RawArgs: rawArgs,
		Scope:   oc.commandScope(portal, meta, evt),
		Reply:   reply,
	})
}

func (oc *AIClient) emitIntegrationSessionMutation(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	force bool,
	kind integrationruntime.SessionMutationKind,
) {
	if oc == nil || oc.eventRegistry == nil {
		return
	}
	oc.eventRegistry.sessionMutation(ctx, integrationruntime.SessionMutationEvent{
		Client:     oc,
		Portal:     portal,
		Meta:       meta,
		SessionKey: portal.PortalKey.String(),
		Force:      force,
		Kind:       kind,
	})
}

func (oc *AIClient) emitIntegrationFileChanged(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, path string) {
	if oc == nil || oc.eventRegistry == nil {
		return
	}
	oc.eventRegistry.fileChanged(ctx, integrationruntime.FileChangedEvent{
		Client: oc,
		Portal: portal,
		Meta:   meta,
		Path:   path,
	})
}

func (oc *AIClient) notifySessionMutation(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, force bool) {
	if oc == nil || portal == nil || meta == nil {
		return
	}
	ctx = oc.backgroundContext(ctx)
	oc.emitIntegrationSessionMutation(ctx, portal, meta, force, integrationruntime.SessionMutationMessage)
}

func notifyIntegrationFileChanged(ctx context.Context, path string) {
	btc := GetBridgeToolContext(ctx)
	if btc == nil || btc.Client == nil {
		return
	}
	meta := portalMeta(btc.Portal)
	btc.Client.emitIntegrationFileChanged(ctx, btc.Portal, meta, path)
}

func (oc *AIClient) purgeLoginIntegrations(ctx context.Context, login any, bridgeID, loginID string) {
	if oc == nil || oc.purgeRegistry == nil {
		return
	}
	if err := oc.purgeRegistry.purge(ctx, integrationruntime.LoginScope{
		Client:   oc,
		Login:    login,
		BridgeID: bridgeID,
		LoginID:  loginID,
	}); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("integration login purge failed")
	}
}

func integrationPortalRoomType(meta *PortalMetadata) string {
	if kind := moduleRoomKind(meta); kind != "" {
		return kind
	}
	return "ai"
}

func isIntegrationSessionKindAllowed(kind string) bool {
	switch kind {
	case "main", "group", "hook", "node", "other":
		return true
	default:
		// Allow any non-empty module-defined kind (e.g., "cron").
		return strings.TrimSpace(kind) != ""
	}
}

func integrationSessionKind(currentRoomID string, portalRoomID string, meta *PortalMetadata) string {
	if currentRoomID != "" && portalRoomID != "" && portalRoomID == currentRoomID {
		return "main"
	}
	if meta != nil {
		if kind := moduleRoomKind(meta); kind != "" {
			return kind
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
	out := make([]integrationruntime.ToolDefinition, 0, len(BuiltinTools()))
	out = append(out, BuiltinTools()...)
	return out
}

func (c *coreToolIntegration) ExecuteTool(ctx context.Context, call integrationruntime.ToolCall) (bool, string, error) {
	if c == nil || c.client == nil {
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
