package connector

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

	"github.com/beeper/ai-bridge/pkg/agents"
	integrationruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
	"github.com/beeper/ai-bridge/pkg/textfs"
)

type runtimeIntegrationHost struct {
	client *AIClient
}

func newRuntimeIntegrationHost(client *AIClient) *runtimeIntegrationHost {
	return &runtimeIntegrationHost{client: client}
}

// ---- Core Host interface ----

func (h *runtimeIntegrationHost) Logger() integrationruntime.Logger {
	return &runtimeLogger{client: h.client}
}

func (h *runtimeIntegrationHost) Now() time.Time { return time.Now() }

func (h *runtimeIntegrationHost) StoreBackend() integrationruntime.StoreBackend {
	if h == nil || h.client == nil {
		return nil
	}
	return &hostStoreBackend{backend: &lazyStoreBackend{client: h.client}}
}

func (h *runtimeIntegrationHost) PortalResolver() integrationruntime.PortalResolver {
	if h == nil || h.client == nil {
		return nil
	}
	return &hostPortalResolver{client: h.client}
}

func (h *runtimeIntegrationHost) Dispatch() integrationruntime.Dispatch {
	if h == nil || h.client == nil {
		return nil
	}
	return &hostDispatch{client: h.client}
}

func (h *runtimeIntegrationHost) SessionStore() integrationruntime.SessionStore {
	if h == nil || h.client == nil {
		return nil
	}
	return &hostSessionStore{client: h.client}
}

func (h *runtimeIntegrationHost) Heartbeat() integrationruntime.Heartbeat {
	if h == nil || h.client == nil {
		return nil
	}
	return &hostHeartbeat{client: h.client}
}

func (h *runtimeIntegrationHost) ToolExec() integrationruntime.ToolExec {
	if h == nil || h.client == nil {
		return nil
	}
	return &hostToolExec{client: h.client}
}

func (h *runtimeIntegrationHost) PromptContext() integrationruntime.PromptContext {
	return &hostPromptContext{}
}

func (h *runtimeIntegrationHost) DBAccess() integrationruntime.DBAccess {
	if h == nil || h.client == nil {
		return nil
	}
	return &hostDBAccess{client: h.client}
}

func (h *runtimeIntegrationHost) ConfigLookup() integrationruntime.ConfigLookup { return h }

// ---- ConfigLookup ----

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

// ---- Optional Host capability: RawLoggerAccess ----

func (h *runtimeIntegrationHost) RawLogger() any {
	if h == nil || h.client == nil {
		return zerolog.Logger{}
	}
	return h.client.log
}

// ---- Optional Host capability: PortalManager ----

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
	if err := p.Save(ctx); err != nil {
		return nil, "", fmt.Errorf("failed to save portal: %w", err)
	}
	chatInfo := &bridgev2.ChatInfo{Name: &p.Name}
	if err := p.CreateMatrixRoom(ctx, h.client.UserLogin, chatInfo); err != nil {
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

// ---- Optional Host capability: MetadataAccess ----

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
	if m.ModuleMeta == nil {
		m.ModuleMeta = make(map[string]any)
	}
	m.ModuleMeta[key] = value
}

func (h *runtimeIntegrationHost) IsSimpleMode(meta any) bool {
	m, _ := meta.(*PortalMetadata)
	return isSimpleMode(m)
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
	return m.IsBuilderRoom || isModuleInternalRoom(m)
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
	case "AgentID":
		if v, ok := value.(string); ok {
			m.AgentID = v
		}
	case "Model":
		if v, ok := value.(string); ok {
			m.Model = strings.TrimSpace(v)
		}
	case "ReasoningEffort":
		if v, ok := value.(string); ok {
			m.ReasoningEffort = strings.TrimSpace(v)
		}
	case "DisabledTools":
		if v, ok := value.([]string); ok {
			m.DisabledTools = v
		}
	}
}

// ---- Optional Host capability: MessageHelper ----

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

// ---- Optional Host capability: HeartbeatHelper ----

func (h *runtimeIntegrationHost) RunHeartbeatOnce(ctx context.Context, reason string) (status string, reasonMsg string) {
	if h == nil || h.client == nil || h.client.heartbeatRunner == nil {
		return "skipped", "disabled"
	}
	res := h.client.heartbeatRunner.run(reason)
	return res.Status, res.Reason
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
	enqueueSystemEvent(sessionKey, text, agentID)
}

func (h *runtimeIntegrationHost) PersistSystemEvents() {
	if h == nil || h.client == nil {
		return
	}
	persistSystemEventsSnapshot(h.client.bridgeStateBackend(), h.client.Log())
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

// ---- Optional Host capability: AgentHelper ----

func (h *runtimeIntegrationHost) ResolveAgentID(raw string, fallbackDefault string) string {
	if h == nil || h.client == nil {
		return agents.DefaultAgentID
	}
	normalized := normalizeAgentID(raw)
	if normalized == "" || !h.agentExists(normalized) {
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
	return h.agentExists(normalizedID)
}

func (h *runtimeIntegrationHost) agentExists(normalizedID string) bool {
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

// ---- Optional Host capability: ModelHelper ----

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

// ---- Optional Host capability: ContextHelper ----

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

// ---- Optional Host capability: ChatCompletionAPI ----

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

// ---- Optional Host capability: ToolPolicyHelper ----

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
	out := make([]integrationruntime.ToolDefinition, 0, len(BuiltinTools()))
	out = append(out, BuiltinTools()...)
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
	params := ToOpenAIChatTools(bridgeTools, &h.client.log)
	return dedupeChatToolParams(params)
}

// ---- Optional Host capability: TextFileHelper ----

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

// ---- Optional Host capability: EmbeddingHelper ----

func (h *runtimeIntegrationHost) ResolveOpenAIEmbeddingConfig(apiKey string, baseURL string, headers map[string]string) (string, string, map[string]string) {
	if h == nil || h.client == nil {
		return apiKey, baseURL, headers
	}
	return resolveEmbeddingConfigGeneric(h.client, apiKey, baseURL, headers, serviceOpenRouter, "/openrouter/v1")
}

func (h *runtimeIntegrationHost) ResolveDirectOpenAIEmbeddingConfig(apiKey string, baseURL string, headers map[string]string) (string, string, map[string]string) {
	if h == nil || h.client == nil {
		return apiKey, baseURL, headers
	}
	return resolveEmbeddingConfigGeneric(h.client, apiKey, baseURL, headers, serviceOpenAI, "/openai/v1")
}

func (h *runtimeIntegrationHost) ResolveGeminiEmbeddingConfig(apiKey string, baseURL string, headers map[string]string) (string, string, map[string]string) {
	return apiKey, baseURL, headers
}

// ---- Optional Host capability: OverflowHelper ----

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

// ---- Optional Host capability: LoginHelper ----

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
		if resolveAgentID(meta) != agentID {
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

// ---- Core Host sub-adapters ----

type hostStoreBackend struct {
	backend *lazyStoreBackend
}

func (s *hostStoreBackend) Read(ctx context.Context, key string) ([]byte, bool, error) {
	if s == nil || s.backend == nil {
		return nil, false, fmt.Errorf("store not available")
	}
	return s.backend.Read(ctx, key)
}

func (s *hostStoreBackend) Write(ctx context.Context, key string, data []byte) error {
	if s == nil || s.backend == nil {
		return fmt.Errorf("store not available")
	}
	return s.backend.Write(ctx, key, data)
}

func (s *hostStoreBackend) List(ctx context.Context, prefix string) ([]integrationruntime.StoreEntry, error) {
	if s == nil || s.backend == nil {
		return nil, fmt.Errorf("store not available")
	}
	entries, err := s.backend.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]integrationruntime.StoreEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, integrationruntime.StoreEntry{Key: e.Key, Data: e.Data})
	}
	return out, nil
}

type hostPortalResolver struct {
	client *AIClient
}

func (r *hostPortalResolver) ResolvePortalByRoomID(ctx context.Context, roomID string) any {
	if r == nil || r.client == nil || strings.TrimSpace(roomID) == "" {
		return nil
	}
	return r.client.portalByRoomID(ctx, portalRoomIDFromString(roomID))
}

func (r *hostPortalResolver) ResolveDefaultPortal(ctx context.Context) any {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.defaultChatPortal()
}

func (r *hostPortalResolver) ResolveLastActivePortal(ctx context.Context, agentID string) any {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.lastActivePortal(agentID)
}

type hostDispatch struct {
	client *AIClient
}

func (d *hostDispatch) DispatchInternalMessage(ctx context.Context, portal any, meta any, message string, source string) error {
	if d == nil || d.client == nil {
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
	_, _, err := d.client.dispatchInternalMessage(ctx, p, m, message, source, false)
	return err
}

func (d *hostDispatch) SendAssistantMessage(ctx context.Context, portal any, body string) error {
	if d == nil || d.client == nil {
		return fmt.Errorf("missing client")
	}
	p, _ := portal.(*bridgev2.Portal)
	if p == nil {
		return fmt.Errorf("missing portal")
	}
	return d.client.sendPlainAssistantMessageWithResult(ctx, p, body)
}

type hostSessionStore struct {
	client *AIClient
}

func (s *hostSessionStore) Update(ctx context.Context, key string, updater func(raw map[string]any) map[string]any) {
	if s == nil || s.client == nil || updater == nil {
		return
	}
	backend := s.client.bridgeStateBackend()
	if backend == nil {
		return
	}
	updateSessionStoreEntry(ctx, backend, key, updater)
}

type hostHeartbeat struct {
	client *AIClient
}

func (hb *hostHeartbeat) RequestNow(ctx context.Context, reason string) {
	if hb == nil || hb.client == nil || hb.client.heartbeatWake == nil {
		return
	}
	hb.client.heartbeatWake.Request(reason, 0)
}

type hostToolExec struct {
	client *AIClient
}

func (t *hostToolExec) ToolDefinitionByName(name string) (integrationruntime.ToolDefinition, bool) {
	for _, def := range BuiltinTools() {
		if def.Name == name {
			return def, true
		}
	}
	return integrationruntime.ToolDefinition{}, false
}

func (t *hostToolExec) ExecuteBuiltinTool(ctx context.Context, scope integrationruntime.ToolScope, name string, rawArgsJSON string) (string, error) {
	if t == nil || t.client == nil {
		return "", fmt.Errorf("missing client")
	}
	portal, _ := scope.Portal.(*bridgev2.Portal)
	return t.client.executeBuiltinTool(ctx, portal, name, rawArgsJSON)
}

type hostPromptContext struct{}

func (p *hostPromptContext) ResolveWorkspaceDir() string {
	return resolvePromptWorkspaceDir()
}

type hostDBAccess struct {
	client *AIClient
}

func (d *hostDBAccess) BridgeDB() any {
	if d == nil || d.client == nil {
		return nil
	}
	return d.client.bridgeDB()
}

func (d *hostDBAccess) BridgeID() string {
	if d == nil || d.client == nil || d.client.UserLogin == nil || d.client.UserLogin.Bridge == nil || d.client.UserLogin.Bridge.DB == nil {
		return ""
	}
	return string(d.client.UserLogin.Bridge.DB.BridgeID)
}

func (d *hostDBAccess) LoginID() string {
	if d == nil || d.client == nil || d.client.UserLogin == nil {
		return ""
	}
	return string(d.client.UserLogin.ID)
}

// ---- Logger ----

type runtimeLogger struct {
	client *AIClient
}

func (l *runtimeLogger) emit(level string, msg string, fields map[string]any) {
	if l == nil || l.client == nil {
		return
	}
	logger := l.client.log.With().Fields(fields).Logger()
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

func (l *runtimeLogger) Debug(msg string, fields map[string]any) { l.emit("debug", msg, fields) }
func (l *runtimeLogger) Info(msg string, fields map[string]any)  { l.emit("info", msg, fields) }
func (l *runtimeLogger) Warn(msg string, fields map[string]any)  { l.emit("warn", msg, fields) }
func (l *runtimeLogger) Error(msg string, fields map[string]any) { l.emit("error", msg, fields) }

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

func resolveEmbeddingConfigGeneric(client *AIClient, apiKey string, baseURL string, headers map[string]string, serviceName string, proxyPath string) (string, string, map[string]string) {
	if strings.TrimSpace(apiKey) == "" && client != nil && client.connector != nil {
		meta := loginMetadata(client.UserLogin)
		apiKey = strings.TrimSpace(client.connector.resolveOpenAIAPIKey(meta))
		if meta != nil {
			if apiKey == "" && meta.Provider == ProviderMagicProxy {
				apiKey = strings.TrimSpace(meta.APIKey)
			}
			if apiKey == "" && meta.Provider == ProviderBeeper {
				services := client.connector.resolveServiceConfig(meta)
				if svc, ok := services[serviceName]; ok {
					apiKey = strings.TrimSpace(svc.APIKey)
					if baseURL == "" {
						baseURL = strings.TrimSpace(svc.BaseURL)
					}
				}
			}
		}
	}
	if strings.TrimSpace(baseURL) == "" && client != nil && client.connector != nil {
		if meta := loginMetadata(client.UserLogin); meta != nil {
			if meta.Provider == ProviderMagicProxy {
				base := normalizeMagicProxyBaseURL(meta.BaseURL)
				if base != "" {
					baseURL = joinProxyPath(base, proxyPath)
				}
			} else if meta.Provider == ProviderBeeper {
				services := client.connector.resolveServiceConfig(meta)
				if svc, ok := services[serviceName]; ok && strings.TrimSpace(svc.BaseURL) != "" {
					baseURL = strings.TrimSpace(svc.BaseURL)
				}
			}
		}
		if baseURL == "" {
			baseURL = client.connector.resolveOpenAIBaseURL()
		}
	}
	return apiKey, baseURL, headers
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

func updateSessionStoreEntry(ctx context.Context, backend bridgeStoreBackend, key string, updater func(raw map[string]any) map[string]any) {
	if backend == nil || updater == nil || strings.TrimSpace(key) == "" {
		return
	}
	storeKey := "session:" + key
	existing := make(map[string]any)
	if data, ok, err := backend.Read(ctx, storeKey); err == nil && ok && len(data) > 0 {
		_ = json.Unmarshal(data, &existing)
	}
	updated := updater(existing)
	if updated == nil {
		return
	}
	data, err := json.Marshal(updated)
	if err != nil {
		return
	}
	_ = backend.Write(ctx, storeKey, data)
}
