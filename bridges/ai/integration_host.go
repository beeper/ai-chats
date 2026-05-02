package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote/pkg/agents"
	integrationruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
	airuntime "github.com/beeper/agentremote/pkg/runtime"
)

type runtimeIntegrationHost struct {
	client *AIClient
}

func (h *runtimeIntegrationHost) ModuleConfig(name string) map[string]any {
	if h == nil || h.client == nil || h.client.connector == nil {
		return nil
	}
	return h.client.integrationModuleConfig(name)
}

func (h *runtimeIntegrationHost) AgentModuleConfig(agentID string, module string) map[string]any {
	if h == nil || h.client == nil || h.client.connector == nil {
		return nil
	}
	return h.client.agentModuleConfig(agentID, module)
}

// ---- Host methods: logger access ----

func (h *runtimeIntegrationHost) RawLogger() zerolog.Logger {
	if h == nil || h.client == nil {
		return zerolog.Logger{}
	}
	return h.client.log
}

func (h *runtimeIntegrationHost) SavePortal(ctx context.Context, portal *bridgev2.Portal, reason string) error {
	if h == nil || h.client == nil {
		return nil
	}
	if portal == nil {
		return nil
	}
	h.client.savePortalQuiet(ctx, portal, reason)
	return nil
}

func (h *runtimeIntegrationHost) IsGroupChat(ctx context.Context, portal *bridgev2.Portal) bool {
	if h == nil || h.client == nil {
		return false
	}
	if portal == nil {
		return false
	}
	return h.client.isGroupChat(ctx, portal)
}

// ---- Host methods: message helpers ----

func (h *runtimeIntegrationHost) RecentMessages(ctx context.Context, portal *bridgev2.Portal, count int) []integrationruntime.MessageSummary {
	if h == nil || h.client == nil {
		return nil
	}
	if portal == nil || count <= 0 || h.client.UserLogin == nil || h.client.UserLogin.Bridge == nil || h.client.UserLogin.Bridge.DB == nil {
		return nil
	}
	maxMessages := count
	if maxMessages > 10 {
		maxMessages = 10
	}
	history, err := h.client.getAIHistoryMessages(h.client.backgroundContext(ctx), portal, maxMessages)
	if err != nil || len(history) == 0 {
		return nil
	}
	return summarizeMessages(history)
}

func (h *runtimeIntegrationHost) SessionTranscript(ctx context.Context, portalKey networkid.PortalKey) ([]integrationruntime.MessageSummary, error) {
	if h == nil || h.client == nil || h.client.UserLogin == nil || h.client.UserLogin.Bridge == nil || h.client.UserLogin.Bridge.DB == nil {
		return nil, nil
	}
	const maxSessionTranscriptMessages = 500
	portal, err := h.client.UserLogin.Bridge.GetPortalByKey(h.client.backgroundContext(ctx), portalKey)
	if err != nil || portal == nil {
		return nil, err
	}
	history, err := h.client.getAIHistoryMessages(h.client.backgroundContext(ctx), portal, maxSessionTranscriptMessages)
	if err != nil || len(history) == 0 {
		return nil, err
	}
	return summarizeMessages(history), nil
}

func summarizeMessages(history []*database.Message) []integrationruntime.MessageSummary {
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
		out = append(out, integrationruntime.MessageSummary{
			Role:               role,
			Body:               text,
			AgentID:            strings.TrimSpace(meta.AgentID),
			ExcludeFromHistory: meta.ExcludeFromHistory,
		})
	}
	return out
}

// ---- Host methods: agent helpers ----

func (h *runtimeIntegrationHost) ResolveAgentID(raw string) string {
	if h == nil || h.client == nil {
		return agents.DefaultAgentID
	}
	normalized := normalizeAgentID(raw)
	if normalized == "" || h.client.connector == nil || h.client.connector.Config.Agents == nil {
		return normalizeAgentID(agents.DefaultAgentID)
	}
	found := false
	for _, entry := range h.client.connector.Config.Agents.List {
		if normalizeAgentID(entry.ID) == normalized {
			found = true
			break
		}
	}
	if !found {
		return normalizeAgentID(agents.DefaultAgentID)
	}
	return normalized
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

// ---- Host methods: model helpers ----

func (h *runtimeIntegrationHost) EffectiveModel(meta integrationruntime.Meta) string {
	if h == nil || h.client == nil {
		return ""
	}
	m, _ := meta.(*PortalMetadata)
	return h.client.effectiveModel(m)
}

func (h *runtimeIntegrationHost) ContextWindow(meta integrationruntime.Meta) int {
	if h == nil || h.client == nil {
		return 0
	}
	m, _ := meta.(*PortalMetadata)
	return h.client.getModelContextWindow(m)
}

// ---- Host methods: chat completions ----

func (h *runtimeIntegrationHost) NewCompletion(ctx context.Context, model string, messages []openai.ChatCompletionMessageParamUnion, toolParams []openai.ChatCompletionToolUnionParam) (*integrationruntime.CompletionResult, error) {
	if h == nil || h.client == nil {
		return nil, fmt.Errorf("missing client")
	}
	req := openai.ChatCompletionNewParams{
		Model:    h.client.modelIDForAPI(model),
		Messages: messages,
		Tools:    toolParams,
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

func (h *runtimeIntegrationHost) IsToolEnabled(meta integrationruntime.Meta, toolName string) bool {
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

func (h *runtimeIntegrationHost) ExecuteToolInContext(ctx context.Context, portal *bridgev2.Portal, meta integrationruntime.Meta, name string, argsJSON string) (string, error) {
	if h == nil || h.client == nil {
		return "", fmt.Errorf("missing client")
	}
	m, _ := meta.(*PortalMetadata)
	if m != nil && !h.client.isToolEnabled(m, name) {
		return "", fmt.Errorf("tool %s is disabled", name)
	}
	toolCtx := WithBridgeToolContext(ctx, &BridgeToolContext{
		Client: h.client,
		Portal: portal,
		Meta:   m,
	})
	return h.client.executeBuiltinTool(toolCtx, portal, name, argsJSON)
}

func (h *runtimeIntegrationHost) ToolsToOpenAIParams(tools []integrationruntime.ToolDefinition) []openai.ChatCompletionToolUnionParam {
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
	store, err := h.client.textFSStoreForAgent(agentID)
	if err != nil {
		return "", "", false, err
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

func (h *runtimeIntegrationHost) WriteTextFile(ctx context.Context, portal *bridgev2.Portal, meta integrationruntime.Meta, agentID string, mode string, path string, content string, maxBytes int) (finalPath string, err error) {
	if h == nil || h.client == nil {
		return "", fmt.Errorf("storage unavailable")
	}
	store, err := h.client.textFSStoreForAgent(agentID)
	if err != nil {
		return "", err
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
		m, _ := meta.(*PortalMetadata)
		toolCtx := WithBridgeToolContext(ctx, &BridgeToolContext{
			Client: h.client,
			Portal: portal,
			Meta:   m,
		})
		notifyTextFSFileChanges(toolCtx, entry.Path)
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

func (h *runtimeIntegrationHost) SessionPortals(ctx context.Context, agentID string) ([]integrationruntime.SessionPortalInfo, error) {
	if h == nil || h.client == nil || h.client.UserLogin == nil || h.client.UserLogin.Bridge == nil || h.client.UserLogin.Bridge.DB == nil {
		return nil, nil
	}
	targetAgentID := h.ResolveAgentID(agentID)
	targetAgentID = normalizeAgentID(targetAgentID)

	portals, err := h.client.listAllChatPortals(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]integrationruntime.SessionPortalInfo, 0, len(portals))
	for _, portal := range portals {
		if portal == nil || portal.MXID == "" {
			continue
		}
		if portal.Receiver != h.client.UserLogin.ID {
			continue
		}
		meta, ok := portal.Metadata.(*PortalMetadata)
		if !ok || meta == nil || meta.InternalRoom() {
			continue
		}
		portalAgentID := h.ResolveAgentID(resolveAgentID(meta))
		portalAgentID = normalizeAgentID(portalAgentID)
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

// ---- AIClient message helpers (called from sessions_tools.go) ----

func (oc *AIClient) latestAssistantTurnRecord(ctx context.Context, portal *bridgev2.Portal) (*aiTurnRecord, error) {
	if portal == nil || oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.DB == nil {
		return nil, nil
	}
	return withResolvedPortalScopeValue(ctx, oc, portal, func(ctx context.Context, _ *bridgev2.Portal, scope *portalScope) (*aiTurnRecord, error) {
		record, err := ensureAIPortalRecordByScope(ctx, scope)
		if err != nil || record == nil {
			return nil, err
		}
		rows, err := queryAITurnRows(ctx, scope, aiTurnQuery{
			contextEpoch:    record.ContextEpoch,
			hasContextEpoch: true,
			kind:            aiTurnKindConversation,
			roles:           []string{"assistant"},
			limit:           1,
		})
		if err != nil || len(rows) == 0 {
			return nil, err
		}
		return rows[0], nil
	})
}

func (oc *AIClient) lastAssistantTurnCheckpoint(ctx context.Context, portal *bridgev2.Portal) *aiTurnRecord {
	row, err := oc.latestAssistantTurnRecord(ctx, portal)
	if err != nil || row == nil {
		return nil
	}
	return row
}

func (oc *AIClient) waitForAssistantTurnAfter(ctx context.Context, portal *bridgev2.Portal, after *aiTurnRecord) (*database.Message, bool) {
	if portal == nil || oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.DB == nil {
		return nil, false
	}
	row, err := oc.latestAssistantTurnRecord(ctx, portal)
	if err != nil || row == nil {
		return nil, false
	}
	if after != nil {
		if row.ContextEpoch != after.ContextEpoch {
			if row.ContextEpoch <= after.ContextEpoch {
				return nil, false
			}
		} else if row.Sequence != after.Sequence {
			if row.Sequence <= after.Sequence {
				return nil, false
			}
		} else if row.TurnID == after.TurnID {
			return nil, false
		}
	}
	if row.ContextEpoch == 0 && row.Sequence == 0 && row.TurnID == "" {
		return nil, false
	}
	return databaseMessageFromAITurn(portal, row), true
}
