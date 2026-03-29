package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/agents"
	integrationcron "github.com/beeper/agentremote/pkg/integrations/cron"
	integrationruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
	airuntime "github.com/beeper/agentremote/pkg/runtime"
	"github.com/beeper/agentremote/pkg/textfs"
)

type runtimeIntegrationHost struct {
	client *AIClient
}

func newRuntimeIntegrationHost(client *AIClient) *runtimeIntegrationHost {
	return &runtimeIntegrationHost{client: client}
}

// ---- Core Host interface ----

func (h *runtimeIntegrationHost) Logger() integrationruntime.Logger {
	if h == nil || h.client == nil {
		return nil
	}
	return h
}

func (h *runtimeIntegrationHost) Now() time.Time { return time.Now() }

func (h *runtimeIntegrationHost) ModuleEnabled(name string) bool {
	if h == nil || h.client == nil || h.client.connector == nil {
		return true
	}
	cfg := h.client.connector.Config.Integrations
	if cfg == nil || cfg.Modules == nil {
		return true
	}
	normalized := strings.ToLower(strings.TrimSpace(name))
	raw, exists := cfg.Modules[normalized]
	if !exists {
		return true
	}
	switch v := raw.(type) {
	case bool:
		return v
	case map[string]any:
		if enabled, ok := v["enabled"]; ok {
			if b, ok := enabled.(bool); ok {
				return b
			}
		}
		return true
	default:
		return true
	}
}

func (h *runtimeIntegrationHost) ModuleConfig(name string) map[string]any {
	if h == nil || h.client == nil || h.client.connector == nil {
		return nil
	}
	normalized := strings.ToLower(strings.TrimSpace(name))
	// Check integrations-level module config first.
	if cfg := h.client.connector.Config.Integrations; cfg != nil && cfg.Modules != nil {
		if raw := cfg.Modules[normalized]; raw != nil {
			if typed, ok := raw.(map[string]any); ok {
				return typed
			}
		}
	}
	// Fall back to top-level module config.
	if h.client.connector.Config.Modules != nil {
		if raw := h.client.connector.Config.Modules[normalized]; raw != nil {
			if typed, ok := raw.(map[string]any); ok {
				return typed
			}
		}
	}
	return nil
}

func (h *runtimeIntegrationHost) AgentModuleConfig(agentID string, module string) map[string]any {
	if h == nil || h.client == nil || h.client.connector == nil {
		return nil
	}
	store := NewAgentStoreAdapter(h.client)
	agent, err := store.GetAgentByID(h.client.backgroundContext(context.TODO()), agentID)
	if err != nil || agent == nil {
		return nil
	}
	// Marshal the entire agent to a generic map and extract the module key.
	raw, err := json.Marshal(agent)
	if err != nil {
		return nil
	}
	var agentMap map[string]any
	if err := json.Unmarshal(raw, &agentMap); err != nil {
		return nil
	}
	moduleName := strings.ToLower(strings.TrimSpace(module))
	moduleData, ok := agentMap[moduleName].(map[string]any)
	if !ok {
		return nil
	}
	return moduleData
}

// ---- Host methods: logger access ----

func (h *runtimeIntegrationHost) RawLogger() any {
	if h == nil || h.client == nil {
		return zerolog.Logger{}
	}
	return h.client.log
}

// ---- Host methods: portal management ----

func (h *runtimeIntegrationHost) GetOrCreatePortal(ctx context.Context, portalID string, receiver string, displayName string, setupMeta func(meta any)) (portal any, roomID string, err error) {
	if h == nil || h.client == nil || h.client.UserLogin == nil {
		return nil, "", fmt.Errorf("missing login")
	}
	portalKey := portalKeyFromParts(h.client, portalID, receiver)
	p, err := h.client.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return nil, "", err
	}
	if p.MXID != "" {
		return p, p.MXID.String(), nil
	}
	meta := &PortalMetadata{}
	if setupMeta != nil {
		setupMeta(meta)
	}
	p.Metadata = meta
	p.Name = displayName
	p.NameSet = true
	chatInfo := &bridgev2.ChatInfo{Name: &p.Name}
	if err := h.client.materializePortalRoom(ctx, p, chatInfo, portalRoomMaterializeOptions{SaveBefore: true}); err != nil {
		return nil, "", fmt.Errorf("failed to create Matrix room: %w", err)
	}
	return p, p.MXID.String(), nil
}

func (h *runtimeIntegrationHost) SavePortal(ctx context.Context, portal any, reason string) error {
	if h == nil || h.client == nil {
		return nil
	}
	p, _ := portal.(*bridgev2.Portal)
	if p == nil {
		return nil
	}
	h.client.savePortalQuiet(ctx, p, reason)
	return nil
}

func (h *runtimeIntegrationHost) PortalRoomID(portal any) string {
	p, _ := portal.(*bridgev2.Portal)
	if p == nil {
		return ""
	}
	return p.MXID.String()
}

func (h *runtimeIntegrationHost) PortalKeyString(portal any) string {
	p, _ := portal.(*bridgev2.Portal)
	if p == nil {
		return ""
	}
	return p.PortalKey.String()
}

// ---- Host methods: metadata access ----

func (h *runtimeIntegrationHost) GetModuleMeta(meta any, key string) any {
	m, _ := meta.(*PortalMetadata)
	if m == nil || m.ModuleMeta == nil {
		return nil
	}
	return m.ModuleMeta[key]
}

func (h *runtimeIntegrationHost) SetModuleMeta(meta any, key string, value any) {
	m, _ := meta.(*PortalMetadata)
	if m == nil {
		return
	}
	m.SetModuleMeta(key, value)
}

func (h *runtimeIntegrationHost) AgentIDFromMeta(meta any) string {
	m, _ := meta.(*PortalMetadata)
	return resolveAgentID(m)
}

func (h *runtimeIntegrationHost) CompactionCount(meta any) int {
	m, _ := meta.(*PortalMetadata)
	if m == nil {
		return 0
	}
	return m.CompactionCount
}

func (h *runtimeIntegrationHost) IsGroupChat(ctx context.Context, portal any) bool {
	if h == nil || h.client == nil {
		return false
	}
	p, _ := portal.(*bridgev2.Portal)
	if p == nil {
		return false
	}
	return h.client.isGroupChat(ctx, p)
}

func (h *runtimeIntegrationHost) IsInternalRoom(meta any) bool {
	m, _ := meta.(*PortalMetadata)
	if m == nil {
		return false
	}
	return isModuleInternalRoom(m)
}

func (h *runtimeIntegrationHost) PortalMeta(portal any) any {
	p, _ := portal.(*bridgev2.Portal)
	return portalMeta(p)
}

func (h *runtimeIntegrationHost) CloneMeta(portal any) any {
	p, _ := portal.(*bridgev2.Portal)
	return clonePortalMetadata(portalMeta(p))
}

func (h *runtimeIntegrationHost) SetMetaField(meta any, key string, value any) {
	m, _ := meta.(*PortalMetadata)
	if m == nil {
		return
	}
	switch key {
	case "DisabledTools":
		if v, ok := value.([]string); ok {
			m.DisabledTools = v
		}
	}
}

// ---- Host methods: message helpers ----

func (h *runtimeIntegrationHost) RecentMessages(ctx context.Context, portal any, count int) []integrationruntime.MessageSummary {
	if h == nil || h.client == nil {
		return nil
	}
	p, _ := portal.(*bridgev2.Portal)
	if p == nil || count <= 0 || h.client.UserLogin == nil || h.client.UserLogin.Bridge == nil || h.client.UserLogin.Bridge.DB == nil {
		return nil
	}
	maxMessages := count
	if maxMessages > 10 {
		maxMessages = 10
	}
	history, err := h.client.UserLogin.Bridge.DB.Message.GetLastNInPortal(h.client.backgroundContext(ctx), p.PortalKey, maxMessages)
	if err != nil || len(history) == 0 {
		return nil
	}
	out := make([]integrationruntime.MessageSummary, 0, len(history))
	for i := len(history) - 1; i >= 0; i-- {
		meta := messageMeta(history[i])
		if meta == nil {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(meta.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		text := strings.TrimSpace(meta.Body)
		if text == "" {
			continue
		}
		out = append(out, integrationruntime.MessageSummary{Role: role, Body: text})
	}
	return out
}

func (h *runtimeIntegrationHost) LastAssistantMessage(ctx context.Context, portal any) (id string, timestamp int64) {
	if h == nil || h.client == nil {
		return "", 0
	}
	p, _ := portal.(*bridgev2.Portal)
	return h.client.lastAssistantMessageInfo(ctx, p)
}

func (h *runtimeIntegrationHost) WaitForAssistantMessage(ctx context.Context, portal any, afterID string, afterTS int64) (*integrationruntime.AssistantMessageInfo, bool) {
	if h == nil || h.client == nil {
		return nil, false
	}
	p, _ := portal.(*bridgev2.Portal)
	msg, found := h.client.waitForNewAssistantMessage(ctx, p, afterID, afterTS)
	if !found || msg == nil {
		return nil, false
	}
	meta := messageMeta(msg)
	if meta == nil {
		return nil, false
	}
	return &integrationruntime.AssistantMessageInfo{
		Body:             strings.TrimSpace(meta.Body),
		Model:            strings.TrimSpace(meta.Model),
		PromptTokens:     meta.PromptTokens,
		CompletionTokens: meta.CompletionTokens,
	}, true
}

// ---- Host methods: heartbeat helpers ----

func (h *runtimeIntegrationHost) RunHeartbeatOnce(ctx context.Context, reason string) (status string, reasonMsg string) {
	if h == nil || h.client == nil || h.client.scheduler == nil {
		return "skipped", "disabled"
	}
	return h.client.scheduler.RunHeartbeatSweep(ctx, reason)
}

func (h *runtimeIntegrationHost) ResolveHeartbeatSessionPortal(agentID string) (portal any, sessionKey string, err error) {
	if h == nil || h.client == nil {
		return nil, "", fmt.Errorf("missing client")
	}
	hb := resolveHeartbeatConfig(&h.client.connector.Config, agentID)
	p, sk, e := h.client.resolveHeartbeatSessionPortal(agentID, hb)
	return p, sk, e
}

func (h *runtimeIntegrationHost) ResolveHeartbeatSessionKey(agentID string) string {
	if h == nil || h.client == nil {
		return ""
	}
	hb := resolveHeartbeatConfig(&h.client.connector.Config, agentID)
	return strings.TrimSpace(h.client.resolveHeartbeatSession(agentID, hb).SessionKey)
}

func (h *runtimeIntegrationHost) HeartbeatAckMaxChars(agentID string) int {
	if h == nil || h.client == nil {
		return 0
	}
	hb := resolveHeartbeatConfig(&h.client.connector.Config, agentID)
	return resolveHeartbeatAckMaxChars(&h.client.connector.Config, hb)
}

func (h *runtimeIntegrationHost) EnqueueSystemEvent(sessionKey string, text string, agentID string) {
	if h == nil || h.client == nil {
		return
	}
	enqueueSystemEvent(systemEventsOwnerKey(h.client), sessionKey, text, agentID)
}

func (h *runtimeIntegrationHost) PersistSystemEvents() {
	if h == nil || h.client == nil {
		return
	}
	persistSystemEventsSnapshot(h.client)
}

func (h *runtimeIntegrationHost) ResolveLastTarget(agentID string) (channel string, target string, ok bool) {
	if h == nil || h.client == nil {
		return "", "", false
	}
	storeRef, mainKey := h.client.resolveHeartbeatMainSessionRef(agentID)
	entry, found := h.client.getSessionEntry(context.Background(), storeRef, mainKey)
	if !found {
		return "", "", false
	}
	return entry.LastChannel, entry.LastTo, true
}

// ---- Host methods: agent helpers ----

func (h *runtimeIntegrationHost) ResolveAgentID(raw string, fallbackDefault string) string {
	if h == nil || h.client == nil {
		return agents.DefaultAgentID
	}
	normalized := normalizeAgentID(raw)
	if normalized == "" || !h.AgentExists(normalized) {
		if fallbackDefault != "" {
			return normalizeAgentID(fallbackDefault)
		}
		return agents.DefaultAgentID
	}
	return normalized
}

func (h *runtimeIntegrationHost) NormalizeAgentID(raw string) string {
	return normalizeAgentID(raw)
}

func (h *runtimeIntegrationHost) AgentExists(normalizedID string) bool {
	if h == nil || h.client == nil || h.client.connector == nil {
		return false
	}
	cfg := &h.client.connector.Config
	if cfg.Agents == nil {
		return false
	}
	for _, entry := range cfg.Agents.List {
		if normalizeAgentID(entry.ID) == strings.TrimSpace(normalizedID) {
			return true
		}
	}
	return false
}

func (h *runtimeIntegrationHost) DefaultAgentID() string {
	return agents.DefaultAgentID
}

func (h *runtimeIntegrationHost) AgentTimeoutSeconds() int {
	if h == nil || h.client == nil || h.client.connector == nil {
		return 600
	}
	cfg := &h.client.connector.Config
	if cfg.Agents != nil && cfg.Agents.Defaults != nil && cfg.Agents.Defaults.TimeoutSeconds > 0 {
		return cfg.Agents.Defaults.TimeoutSeconds
	}
	return 600
}

func (h *runtimeIntegrationHost) UserTimezone() (tz string, loc *time.Location) {
	if h == nil || h.client == nil {
		return "", time.UTC
	}
	tz, loc = h.client.resolveUserTimezone()
	if loc == nil {
		loc = time.UTC
	}
	return tz, loc
}

func (h *runtimeIntegrationHost) NormalizeThinkingLevel(raw string) (string, bool) {
	return normalizeThinkingLevel(raw)
}

// ---- Host methods: model helpers ----

func (h *runtimeIntegrationHost) EffectiveModel(meta any) string {
	if h == nil || h.client == nil {
		return ""
	}
	m, _ := meta.(*PortalMetadata)
	return h.client.effectiveModel(m)
}

func (h *runtimeIntegrationHost) ContextWindow(meta any) int {
	if h == nil || h.client == nil {
		return 0
	}
	m, _ := meta.(*PortalMetadata)
	return h.client.getModelContextWindow(m)
}

// ---- Host methods: context helpers ----

func (h *runtimeIntegrationHost) MergeDisconnectContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if h == nil || h.client == nil {
		return context.WithCancel(ctx)
	}
	var base context.Context
	if h.client.disconnectCtx != nil {
		base = h.client.disconnectCtx
	} else if h.client.UserLogin != nil && h.client.UserLogin.Bridge != nil && h.client.UserLogin.Bridge.BackgroundCtx != nil {
		base = h.client.UserLogin.Bridge.BackgroundCtx
	} else {
		base = context.Background()
	}
	if model, ok := modelOverrideFromContext(ctx); ok {
		base = withModelOverride(base, model)
	}
	var merged context.Context
	var cancel context.CancelFunc
	if deadline, ok := ctx.Deadline(); ok {
		merged, cancel = context.WithDeadline(base, deadline)
	} else {
		merged, cancel = context.WithCancel(base)
	}
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-merged.Done():
		}
	}()
	return h.client.loggerForContext(ctx).WithContext(merged), cancel
}

func (h *runtimeIntegrationHost) BackgroundContext(ctx context.Context) context.Context {
	if h == nil || h.client == nil {
		return ctx
	}
	return h.client.backgroundContext(ctx)
}

// ---- Host methods: chat completions ----

func (h *runtimeIntegrationHost) NewCompletion(ctx context.Context, model string, messages []openai.ChatCompletionMessageParamUnion, toolParams any) (*integrationruntime.CompletionResult, error) {
	if h == nil || h.client == nil {
		return nil, fmt.Errorf("missing client")
	}
	params, _ := toolParams.([]openai.ChatCompletionToolUnionParam)
	req := openai.ChatCompletionNewParams{
		Model:    model,
		Messages: messages,
		Tools:    params,
	}
	resp, err := h.client.api.Chat.Completions.New(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return &integrationruntime.CompletionResult{Done: true}, nil
	}
	msg := resp.Choices[0].Message
	assistant := msg.ToAssistantMessageParam()
	result := &integrationruntime.CompletionResult{
		AssistantMessage: openai.ChatCompletionMessageParamUnion{OfAssistant: &assistant},
	}
	if len(msg.ToolCalls) == 0 {
		result.Done = true
	} else {
		calls := make([]integrationruntime.CompletionToolCall, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			calls = append(calls, integrationruntime.CompletionToolCall{
				ID:       tc.ID,
				Name:     strings.TrimSpace(tc.Function.Name),
				ArgsJSON: tc.Function.Arguments,
			})
		}
		result.ToolCalls = calls
	}
	return result, nil
}

// ---- Host methods: tool policy ----

func (h *runtimeIntegrationHost) IsToolEnabled(meta any, toolName string) bool {
	if h == nil || h.client == nil {
		return true
	}
	m, _ := meta.(*PortalMetadata)
	if m == nil {
		return true
	}
	return h.client.isToolEnabled(m, toolName)
}

func (h *runtimeIntegrationHost) AllToolDefinitions() []integrationruntime.ToolDefinition {
	defs := BuiltinTools()
	out := make([]integrationruntime.ToolDefinition, 0, len(defs))
	out = append(out, defs...)
	return out
}

func (h *runtimeIntegrationHost) ExecuteToolInContext(ctx context.Context, portal any, meta any, name string, argsJSON string) (string, error) {
	if h == nil || h.client == nil {
		return "", fmt.Errorf("missing client")
	}
	p, _ := portal.(*bridgev2.Portal)
	m, _ := meta.(*PortalMetadata)
	if m != nil && !h.client.isToolEnabled(m, name) {
		return "", fmt.Errorf("tool %s is disabled", name)
	}
	toolCtx := WithBridgeToolContext(ctx, &BridgeToolContext{
		Client: h.client,
		Portal: p,
		Meta:   m,
	})
	return h.client.executeBuiltinTool(toolCtx, p, name, argsJSON)
}

func (h *runtimeIntegrationHost) ToolsToOpenAIParams(tools []integrationruntime.ToolDefinition) any {
	if h == nil || h.client == nil {
		return nil
	}
	bridgeTools := make([]ToolDefinition, 0, len(tools))
	bridgeTools = append(bridgeTools, tools...)
	params := ToOpenAIChatTools(bridgeTools, resolveToolStrictMode(h.client.isOpenRouterProvider()), &h.client.log)
	return dedupeChatToolParams(params)
}

// ---- Host methods: text file access ----

func (h *runtimeIntegrationHost) ReadTextFile(ctx context.Context, agentID string, path string) (content string, filePath string, found bool, err error) {
	if h == nil || h.client == nil {
		return "", "", false, fmt.Errorf("storage unavailable")
	}
	store := textStoreForAgent(h.client, agentID)
	if store == nil {
		return "", "", false, fmt.Errorf("storage unavailable")
	}
	entry, ok, e := store.Read(ctx, path)
	if e != nil {
		return "", "", false, e
	}
	if !ok {
		return "", "", false, nil
	}
	return entry.Content, entry.Path, true, nil
}

func (h *runtimeIntegrationHost) WriteTextFile(ctx context.Context, portal any, meta any, agentID string, mode string, path string, content string, maxBytes int) (finalPath string, err error) {
	if h == nil || h.client == nil {
		return "", fmt.Errorf("storage unavailable")
	}
	store := textStoreForAgent(h.client, agentID)
	if store == nil {
		return "", fmt.Errorf("storage unavailable")
	}
	if len([]byte(content)) > maxBytes {
		return "", fmt.Errorf("content exceeds %d bytes", maxBytes)
	}
	if strings.EqualFold(strings.TrimSpace(mode), "append") {
		if existing, ok, e := store.Read(ctx, path); e != nil {
			return "", fmt.Errorf("failed to read existing file for append: %w", e)
		} else if ok {
			sep := "\n"
			if strings.HasSuffix(existing.Content, "\n") || existing.Content == "" {
				sep = ""
			}
			content = existing.Content + sep + content
			if len([]byte(content)) > maxBytes {
				return "", fmt.Errorf("content exceeds %d bytes after append", maxBytes)
			}
		}
	}
	entry, e := store.Write(ctx, path, content)
	if e != nil {
		return "", e
	}
	if entry != nil {
		p, _ := portal.(*bridgev2.Portal)
		m, _ := meta.(*PortalMetadata)
		toolCtx := WithBridgeToolContext(ctx, &BridgeToolContext{
			Client: h.client,
			Portal: p,
			Meta:   m,
		})
		notifyIntegrationFileChanged(toolCtx, entry.Path)
		maybeRefreshAgentIdentity(toolCtx, entry.Path)
		return entry.Path, nil
	}
	return path, nil
}

// ---- Host methods: overflow helpers ----

func (h *runtimeIntegrationHost) SmartTruncatePrompt(prompt []openai.ChatCompletionMessageParamUnion, ratio float64) []openai.ChatCompletionMessageParamUnion {
	return airuntime.SmartTruncatePrompt(prompt, ratio)
}

func (h *runtimeIntegrationHost) EstimateTokens(prompt []openai.ChatCompletionMessageParamUnion, model string) int {
	if len(prompt) == 0 {
		return 0
	}
	if count, err := EstimateTokens(prompt, model); err == nil && count > 0 {
		return count
	}
	return estimatePromptTokensFallback(prompt)
}

func (h *runtimeIntegrationHost) CompactorReserveTokens() int {
	if h == nil || h.client == nil {
		return airuntime.DefaultPruningConfig().ReserveTokens
	}
	return h.client.pruningReserveTokens()
}

func (h *runtimeIntegrationHost) SilentReplyToken() string {
	return agents.SilentReplyToken
}

func (h *runtimeIntegrationHost) OverflowFlushConfig() (enabled *bool, softThresholdTokens int, prompt string, systemPrompt string) {
	if h == nil || h.client == nil {
		return nil, 0, "", ""
	}
	cfg := h.client.pruningOverflowFlushConfig()
	if cfg == nil {
		return nil, 0, "", ""
	}
	return cfg.Enabled, cfg.SoftThresholdTokens, cfg.Prompt, cfg.SystemPrompt
}

// ---- Host methods: login helpers ----

func (h *runtimeIntegrationHost) IsLoggedIn() bool {
	if h == nil || h.client == nil {
		return false
	}
	return h.client.IsLoggedIn()
}

func (h *runtimeIntegrationHost) SessionPortals(ctx context.Context, loginID string, agentID string) ([]integrationruntime.SessionPortalInfo, error) {
	if h == nil || h.client == nil || h.client.UserLogin == nil || h.client.UserLogin.Bridge == nil || h.client.UserLogin.Bridge.DB == nil {
		return nil, nil
	}
	if strings.TrimSpace(loginID) == "" {
		loginID = string(h.client.UserLogin.ID)
	}
	targetAgentID := h.ResolveAgentID(agentID, h.DefaultAgentID())
	targetAgentID = h.NormalizeAgentID(targetAgentID)

	allowedShared := map[string]struct{}{}
	ups, err := h.client.UserLogin.Bridge.DB.UserPortal.GetAllForLogin(ctx, h.client.UserLogin.UserLogin)
	if err != nil {
		return nil, err
	}
	for _, up := range ups {
		if up == nil || up.Portal.Receiver != "" {
			continue
		}
		allowedShared[up.Portal.String()] = struct{}{}
	}

	portals, err := h.client.UserLogin.Bridge.DB.Portal.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]integrationruntime.SessionPortalInfo, 0, len(portals))
	for _, portal := range portals {
		if portal == nil || portal.MXID == "" {
			continue
		}
		if portal.Receiver != "" && string(portal.Receiver) != loginID {
			continue
		}
		if portal.Receiver == "" {
			if len(allowedShared) == 0 {
				continue
			}
			if _, ok := allowedShared[portal.PortalKey.String()]; !ok {
				continue
			}
		}
		meta, ok := portal.Metadata.(*PortalMetadata)
		if !ok || meta == nil || isModuleInternalRoom(meta) {
			continue
		}
		portalAgentID := h.ResolveAgentID(resolveAgentID(meta), h.DefaultAgentID())
		portalAgentID = h.NormalizeAgentID(portalAgentID)
		if portalAgentID != targetAgentID {
			continue
		}
		key := portal.PortalKey.String()
		if key == "" {
			continue
		}
		out = append(out, integrationruntime.SessionPortalInfo{Key: key, PortalKey: portal.PortalKey})
	}
	return out, nil
}

func (h *runtimeIntegrationHost) LoginDB() any {
	if h == nil || h.client == nil {
		return nil
	}
	return h.client.bridgeDB()
}

// ---- Host methods: cron scheduler ----

func (h *runtimeIntegrationHost) CronStatus(ctx context.Context) (bool, string, int, *int64, error) {
	if h == nil || h.client == nil || h.client.scheduler == nil {
		return false, "", 0, nil, fmt.Errorf("scheduler not available")
	}
	return h.client.scheduler.CronStatus(ctx)
}

func (h *runtimeIntegrationHost) CronList(ctx context.Context, includeDisabled bool) ([]integrationcron.Job, error) {
	if h == nil || h.client == nil || h.client.scheduler == nil {
		return nil, fmt.Errorf("scheduler not available")
	}
	return h.client.scheduler.CronList(ctx, includeDisabled)
}

func (h *runtimeIntegrationHost) CronAdd(ctx context.Context, input integrationcron.JobCreate) (integrationcron.Job, error) {
	if h == nil || h.client == nil || h.client.scheduler == nil {
		return integrationcron.Job{}, fmt.Errorf("scheduler not available")
	}
	return h.client.scheduler.CronAdd(ctx, input)
}

func (h *runtimeIntegrationHost) CronUpdate(ctx context.Context, jobID string, patch integrationcron.JobPatch) (integrationcron.Job, error) {
	if h == nil || h.client == nil || h.client.scheduler == nil {
		return integrationcron.Job{}, fmt.Errorf("scheduler not available")
	}
	return h.client.scheduler.CronUpdate(ctx, jobID, patch)
}

func (h *runtimeIntegrationHost) CronRemove(ctx context.Context, jobID string) (bool, error) {
	if h == nil || h.client == nil || h.client.scheduler == nil {
		return false, fmt.Errorf("scheduler not available")
	}
	return h.client.scheduler.CronRemove(ctx, jobID)
}

func (h *runtimeIntegrationHost) CronRun(ctx context.Context, jobID string) (bool, string, error) {
	if h == nil || h.client == nil || h.client.scheduler == nil {
		return false, "", fmt.Errorf("scheduler not available")
	}
	return h.client.scheduler.CronRun(ctx, jobID)
}

// ---- Host methods: dispatch/lookup primitives ----

func (h *runtimeIntegrationHost) ResolvePortalByRoomID(ctx context.Context, roomID string) any {
	if h == nil || h.client == nil || strings.TrimSpace(roomID) == "" {
		return nil
	}
	return h.client.portalByRoomID(ctx, portalRoomIDFromString(roomID))
}

func (h *runtimeIntegrationHost) ResolveDefaultPortal(ctx context.Context) any {
	if h == nil || h.client == nil {
		return nil
	}
	return h.client.defaultChatPortal()
}

func (h *runtimeIntegrationHost) ResolveLastActivePortal(ctx context.Context, agentID string) any {
	if h == nil || h.client == nil {
		return nil
	}
	return h.client.lastActivePortal(agentID)
}

func (h *runtimeIntegrationHost) DispatchInternalMessage(ctx context.Context, portal any, meta any, message string, source string) error {
	if h == nil || h.client == nil {
		return fmt.Errorf("missing client")
	}
	p, _ := portal.(*bridgev2.Portal)
	if p == nil {
		return fmt.Errorf("missing portal")
	}
	m, _ := meta.(*PortalMetadata)
	if m == nil {
		m = &PortalMetadata{}
	}
	_, _, err := h.client.dispatchInternalMessage(ctx, p, m, message, source, false)
	return err
}

func (h *runtimeIntegrationHost) SendAssistantMessage(ctx context.Context, portal any, body string) error {
	if h == nil || h.client == nil {
		return fmt.Errorf("missing client")
	}
	p, _ := portal.(*bridgev2.Portal)
	if p == nil {
		return fmt.Errorf("missing portal")
	}
	return h.client.sendPlainAssistantMessage(ctx, p, body)
}

func (h *runtimeIntegrationHost) RequestNow(ctx context.Context, reason string) {
	if h == nil || h.client == nil || h.client.scheduler == nil {
		return
	}
	h.client.scheduler.RequestHeartbeatNow(ctx, reason)
}

func (h *runtimeIntegrationHost) ToolDefinitionByName(name string) (integrationruntime.ToolDefinition, bool) {
	for _, def := range BuiltinTools() {
		if def.Name == name {
			return def, true
		}
	}
	return integrationruntime.ToolDefinition{}, false
}

func (h *runtimeIntegrationHost) ExecuteBuiltinTool(ctx context.Context, scope integrationruntime.ToolScope, name string, rawArgsJSON string) (string, error) {
	if h == nil || h.client == nil {
		return "", fmt.Errorf("missing client")
	}
	portal, _ := scope.Portal.(*bridgev2.Portal)
	meta, _ := scope.Meta.(*PortalMetadata)
	if meta != nil && !h.client.isToolEnabled(meta, name) {
		return "", fmt.Errorf("tool %s is disabled", name)
	}
	toolCtx := WithBridgeToolContext(ctx, &BridgeToolContext{
		Client: h.client,
		Portal: portal,
		Meta:   meta,
	})
	return h.client.executeBuiltinTool(toolCtx, portal, name, rawArgsJSON)
}

func (h *runtimeIntegrationHost) ResolveWorkspaceDir() string {
	return resolvePromptWorkspaceDir()
}

func (h *runtimeIntegrationHost) BridgeDB() any {
	if h == nil || h.client == nil {
		return nil
	}
	return h.client.bridgeDB()
}

func (h *runtimeIntegrationHost) BridgeID() string {
	if h == nil || h.client == nil || h.client.UserLogin == nil || h.client.UserLogin.Bridge == nil || h.client.UserLogin.Bridge.DB == nil {
		return ""
	}
	return string(h.client.UserLogin.Bridge.DB.BridgeID)
}

func (h *runtimeIntegrationHost) LoginID() string {
	if h == nil || h.client == nil || h.client.UserLogin == nil {
		return ""
	}
	return string(h.client.UserLogin.ID)
}

// ---- Logger ----

func (h *runtimeIntegrationHost) emit(level string, msg string, fields map[string]any) {
	if h == nil || h.client == nil {
		return
	}
	logger := h.client.log.With().Fields(fields).Logger()
	switch level {
	case "debug":
		logger.Debug().Msg(msg)
	case "info":
		logger.Info().Msg(msg)
	case "warn":
		logger.Warn().Msg(msg)
	case "error":
		logger.Error().Msg(msg)
	}
}

func (h *runtimeIntegrationHost) Debug(msg string, fields map[string]any) {
	h.emit("debug", msg, fields)
}
func (h *runtimeIntegrationHost) Info(msg string, fields map[string]any) { h.emit("info", msg, fields) }
func (h *runtimeIntegrationHost) Warn(msg string, fields map[string]any) { h.emit("warn", msg, fields) }
func (h *runtimeIntegrationHost) Error(msg string, fields map[string]any) {
	h.emit("error", msg, fields)
}

// ---- AIClient message helpers (called from sessions_tools.go) ----

func (oc *AIClient) lastAssistantMessageInfo(ctx context.Context, portal *bridgev2.Portal) (string, int64) {
	if portal == nil || oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.DB == nil || oc.UserLogin.Bridge.DB.Message == nil {
		return "", 0
	}
	messages, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, 20)
	if err != nil {
		return "", 0
	}
	bestID := ""
	bestTS := int64(0)
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		meta := messageMeta(msg)
		if meta == nil || meta.Role != "assistant" {
			continue
		}
		ts := msg.Timestamp.UnixMilli()
		if bestID == "" || ts > bestTS {
			bestID = msg.MXID.String()
			bestTS = ts
		}
	}
	return bestID, bestTS
}

func (oc *AIClient) waitForNewAssistantMessage(ctx context.Context, portal *bridgev2.Portal, lastID string, lastTimestamp int64) (*database.Message, bool) {
	if portal == nil || oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.DB == nil || oc.UserLogin.Bridge.DB.Message == nil {
		return nil, false
	}
	messages, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, 20)
	if err != nil {
		return nil, false
	}
	var candidate *database.Message
	candidateTS := lastTimestamp
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		meta := messageMeta(msg)
		if meta == nil || meta.Role != "assistant" {
			continue
		}
		idStr := msg.MXID.String()
		ts := msg.Timestamp.UnixMilli()
		if ts < lastTimestamp {
			continue
		}
		if ts == lastTimestamp && idStr == lastID {
			continue
		}
		if candidate == nil || ts > candidateTS {
			candidate = msg
			candidateTS = ts
		}
	}
	if candidate == nil {
		return nil, false
	}
	return candidate, true
}

// ---- Helpers ----

func textStoreForAgent(client *AIClient, agentID string) *textfs.Store {
	if client == nil || client.UserLogin == nil || client.UserLogin.Bridge == nil || client.UserLogin.Bridge.DB == nil {
		return nil
	}
	db := client.bridgeDB()
	if db == nil {
		return nil
	}
	return textfs.NewStore(
		db,
		string(client.UserLogin.Bridge.DB.BridgeID),
		string(client.UserLogin.ID),
		agentID,
	)
}

// ---- Small helpers used by host sub-adapters ----

func portalKeyFromParts(client *AIClient, portalID string, receiver string) networkid.PortalKey {
	key := networkid.PortalKey{ID: networkid.PortalID(portalID)}
	if receiver != "" {
		key.Receiver = networkid.UserLoginID(receiver)
	} else if client != nil && client.UserLogin != nil {
		key.Receiver = client.UserLogin.ID
	}
	return key
}

func portalRoomIDFromString(roomID string) id.RoomID {
	return id.RoomID(roomID)
}
