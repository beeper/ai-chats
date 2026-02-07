package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/google/uuid"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/agents/tools"
)

// AgentStoreAdapter implements agents.AgentStore with UserLogin metadata as source of truth.
type AgentStoreAdapter struct {
	client *AIClient
	mu     sync.Mutex // protects read-modify-write operations on custom agents
}

// NewAgentStoreAdapter creates a new agent store adapter.
func NewAgentStoreAdapter(client *AIClient) *AgentStoreAdapter {
	return &AgentStoreAdapter{client: client}
}

// LoadAgents implements agents.AgentStore.
// It loads agents from presets and metadata-backed custom agents.
func (s *AgentStoreAdapter) LoadAgents(ctx context.Context) (map[string]*agents.AgentDefinition, error) {
	_ = ctx
	// Start with preset agents
	result := make(map[string]*agents.AgentDefinition)
	showBexus := s.client != nil && s.client.hasConnectedClayMCP()

	// Add all presets
	for _, preset := range agents.PresetAgents {
		if preset != nil && agents.IsNexusAI(preset.ID) && !showBexus {
			continue
		}
		result[preset.ID] = preset.Clone()
	}

	// Add boss agent
	result[agents.BossAgent.ID] = agents.BossAgent.Clone()

	for id, content := range s.loadCustomAgentsFromMetadata() {
		result[id] = FromAgentDefinitionContent(content)
	}

	return result, nil
}

func (s *AgentStoreAdapter) loadCustomAgentsFromMetadata() map[string]*AgentDefinitionContent {
	meta := loginMetadata(s.client.UserLogin)
	if meta == nil || len(meta.CustomAgents) == 0 {
		return nil
	}
	result := make(map[string]*AgentDefinitionContent, len(meta.CustomAgents))
	for id, agent := range meta.CustomAgents {
		if agent == nil {
			continue
		}
		result[id] = agent
	}
	return result
}

func (s *AgentStoreAdapter) loadCustomAgentFromMetadata(agentID string) *AgentDefinitionContent {
	meta := loginMetadata(s.client.UserLogin)
	if meta == nil || meta.CustomAgents == nil {
		return nil
	}
	return meta.CustomAgents[agentID]
}

func (s *AgentStoreAdapter) saveAgentToMetadata(ctx context.Context, agent *AgentDefinitionContent) error {
	if agent == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	meta := loginMetadata(s.client.UserLogin)
	if meta.CustomAgents == nil {
		meta.CustomAgents = map[string]*AgentDefinitionContent{}
	}
	meta.CustomAgents[agent.ID] = agent
	return s.client.UserLogin.Save(ctx)
}

func (s *AgentStoreAdapter) deleteAgentFromMetadata(ctx context.Context, agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta := loginMetadata(s.client.UserLogin)
	if meta.CustomAgents == nil {
		return nil
	}
	if _, ok := meta.CustomAgents[agentID]; !ok {
		return nil
	}
	delete(meta.CustomAgents, agentID)
	return s.client.UserLogin.Save(ctx)
}

// SaveAgent implements agents.AgentStore.
// It saves custom agents to UserLogin metadata.
func (s *AgentStoreAdapter) SaveAgent(ctx context.Context, agent *agents.AgentDefinition) error {
	if err := agent.Validate(); err != nil {
		return err
	}
	if agent.IsPreset {
		return agents.ErrAgentIsPreset
	}

	content := ToAgentDefinitionContent(agent)

	if err := s.saveAgentToMetadata(ctx, content); err != nil {
		return fmt.Errorf("failed to save custom agent to metadata store: %w", err)
	}

	s.client.log.Info().Str("agent_id", agent.ID).Str("name", agent.Name).Msg("Saved custom agent to metadata store")
	return nil
}

// DeleteAgent implements agents.AgentStore.
// It deletes a custom agent from UserLogin metadata.
func (s *AgentStoreAdapter) DeleteAgent(ctx context.Context, agentID string) error {
	if agents.IsPreset(agentID) || agents.IsBossAgent(agentID) {
		return agents.ErrAgentIsPreset
	}

	if s.loadCustomAgentFromMetadata(agentID) == nil {
		return agents.ErrAgentNotFound
	}

	if err := s.deleteAgentFromMetadata(ctx, agentID); err != nil {
		return fmt.Errorf("failed to delete custom agent from metadata store: %w", err)
	}

	s.client.log.Info().Str("agent_id", agentID).Msg("Deleted custom agent from metadata store")
	return nil
}

// ListModels implements agents.AgentStore.
func (s *AgentStoreAdapter) ListModels(ctx context.Context) ([]agents.ModelInfo, error) {
	models, err := s.client.listAvailableModels(ctx, false)
	if err != nil {
		return nil, err
	}

	result := make([]agents.ModelInfo, 0, len(models))
	for _, m := range models {
		result = append(result, agents.ModelInfo{
			ID:          m.ID,
			Name:        m.Name,
			Provider:    m.Provider,
			Description: m.Description,
		})
	}
	return result, nil
}

// ListAvailableTools implements agents.AgentStore.
func (s *AgentStoreAdapter) ListAvailableTools(_ context.Context) ([]tools.ToolInfo, error) {
	registry := tools.DefaultRegistry()

	var result []tools.ToolInfo
	for _, tool := range registry.All() {
		result = append(result, tools.ToolInfo{
			Name:        tool.Name,
			Description: tool.Description,
			Type:        tool.Type,
			Group:       tool.Group,
			Enabled:     true, // All tools are available, policy determines which are enabled
		})
	}
	return result, nil
}

// Verify interface compliance
var _ agents.AgentStore = (*AgentStoreAdapter)(nil)

// GetAgentByID looks up an agent by ID, returning preset or custom agents.
func (s *AgentStoreAdapter) GetAgentByID(ctx context.Context, agentID string) (*agents.AgentDefinition, error) {
	agentsMap, err := s.LoadAgents(ctx)
	if err != nil {
		return nil, err
	}

	agent, ok := agentsMap[agentID]
	if !ok {
		return nil, agents.ErrAgentNotFound
	}
	return agent, nil
}

// GetAgentForRoom returns the agent assigned to a room.
// Falls back to the Quick Chatter if no specific agent is set.
func (s *AgentStoreAdapter) GetAgentForRoom(ctx context.Context, meta *PortalMetadata) (*agents.AgentDefinition, error) {
	agentID := resolveAgentID(meta)
	if agentID == "" {
		agentID = agents.DefaultAgentID // Default to Beep
	}

	return s.GetAgentByID(ctx, agentID)
}

// ToAgentDefinitionContent converts an AgentDefinition to its Matrix event form.
func ToAgentDefinitionContent(agent *agents.AgentDefinition) *AgentDefinitionContent {
	content := &AgentDefinitionContent{
		ID:              agent.ID,
		Name:            agent.Name,
		Description:     agent.Description,
		AvatarURL:       agent.AvatarURL,
		Model:           agent.Model.Primary,
		ModelFallback:   agent.Model.Fallbacks,
		SystemPrompt:    agent.SystemPrompt,
		PromptMode:      string(agent.PromptMode),
		Tools:           agent.Tools.Clone(),
		Temperature:     agent.Temperature,
		ReasoningEffort: agent.ReasoningEffort,
		HeartbeatPrompt: agent.HeartbeatPrompt,
		IsPreset:        agent.IsPreset,
		CreatedAt:       agent.CreatedAt,
		UpdatedAt:       agent.UpdatedAt,
	}

	// Include Identity if present
	if agent.Identity != nil {
		content.IdentityName = agent.Identity.Name
		content.IdentityPersona = agent.Identity.Persona
	}

	if agent.MemorySearch != nil {
		content.MemorySearch = agent.MemorySearch
	}

	return content
}

// FromAgentDefinitionContent converts a Matrix event form to AgentDefinition.
func FromAgentDefinitionContent(content *AgentDefinitionContent) *agents.AgentDefinition {
	def := &agents.AgentDefinition{
		ID:          content.ID,
		Name:        content.Name,
		Description: content.Description,
		AvatarURL:   content.AvatarURL,
		Model: agents.ModelConfig{
			Primary:   content.Model,
			Fallbacks: content.ModelFallback,
		},
		SystemPrompt:    content.SystemPrompt,
		PromptMode:      agents.PromptMode(content.PromptMode),
		Tools:           content.Tools.Clone(),
		Temperature:     content.Temperature,
		ReasoningEffort: content.ReasoningEffort,
		HeartbeatPrompt: content.HeartbeatPrompt,
		IsPreset:        content.IsPreset,
		CreatedAt:       content.CreatedAt,
		UpdatedAt:       content.UpdatedAt,
	}

	// Restore Identity if present
	if content.IdentityName != "" || content.IdentityPersona != "" {
		def.Identity = &agents.Identity{
			Name:    content.IdentityName,
			Persona: content.IdentityPersona,
		}
	}

	if content.MemorySearch != nil {
		def.MemorySearch = content.MemorySearch
	}

	return def
}

// BossStoreAdapter implements tools.AgentStoreInterface for boss tool execution.
// This adapter converts between our agent types and the tools package types.
type BossStoreAdapter struct {
	store *AgentStoreAdapter
}

// NewBossStoreAdapter creates a new boss store adapter.
func NewBossStoreAdapter(client *AIClient) *BossStoreAdapter {
	return &BossStoreAdapter{
		store: NewAgentStoreAdapter(client),
	}
}

// LoadAgents implements tools.AgentStoreInterface.
func (b *BossStoreAdapter) LoadAgents(ctx context.Context) (map[string]tools.AgentData, error) {
	agentsMap, err := b.store.LoadAgents(ctx)
	if err != nil {
		return nil, err
	}

	result := make(map[string]tools.AgentData, len(agentsMap))
	for id, agent := range agentsMap {
		result[id] = agentToToolsData(agent)
	}
	return result, nil
}

// SaveAgent implements tools.AgentStoreInterface.
func (b *BossStoreAdapter) SaveAgent(ctx context.Context, agent tools.AgentData) error {
	def := toolsDataToAgent(agent)
	return b.store.SaveAgent(ctx, def)
}

// DeleteAgent implements tools.AgentStoreInterface.
func (b *BossStoreAdapter) DeleteAgent(ctx context.Context, agentID string) error {
	return b.store.DeleteAgent(ctx, agentID)
}

// ListModels implements tools.AgentStoreInterface.
func (b *BossStoreAdapter) ListModels(ctx context.Context) ([]tools.ModelData, error) {
	models, err := b.store.ListModels(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]tools.ModelData, 0, len(models))
	for _, m := range models {
		result = append(result, tools.ModelData{
			ID:          m.ID,
			Name:        m.Name,
			Provider:    m.Provider,
			Description: m.Description,
		})
	}
	return result, nil
}

// ListAvailableTools implements tools.AgentStoreInterface.
func (b *BossStoreAdapter) ListAvailableTools(ctx context.Context) ([]tools.ToolInfo, error) {
	return b.store.ListAvailableTools(ctx)
}

// RunInternalCommand implements tools.AgentStoreInterface.
func (b *BossStoreAdapter) RunInternalCommand(ctx context.Context, roomID string, command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", errors.New("command is required")
	}
	if roomID == "" {
		return "", errors.New("room_id is required")
	}

	prefix := b.store.client.connector.br.Config.CommandPrefix
	if strings.HasPrefix(command, prefix) {
		command = strings.TrimSpace(strings.TrimPrefix(command, prefix))
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return "", errors.New("command is empty after trimming prefix")
	}

	args := strings.Fields(command)
	if len(args) == 0 {
		return "", errors.New("command is empty")
	}
	cmdName := strings.ToLower(args[0])
	rawArgs := strings.TrimLeft(strings.TrimPrefix(command, args[0]), " ")

	handler := aiCommandRegistry.Get(cmdName)
	if handler == nil {
		return "", fmt.Errorf("unknown AI command: %s", cmdName)
	}

	portal, err := b.resolvePortalByRoomID(ctx, roomID)
	if err != nil {
		return "", err
	}
	if portal == nil || portal.MXID == "" {
		return "", fmt.Errorf("room '%s' has no Matrix ID", roomID)
	}

	runCtx := b.store.client.backgroundContext(ctx)
	logCopy := b.store.client.log.With().Str("mx_command", cmdName).Logger()
	captureBot := newCaptureMatrixAPI(b.store.client.UserLogin.Bridge.Bot)
	eventID := id.EventID(fmt.Sprintf("$internal-%s", uuid.NewString()))
	ce := &commands.Event{
		Bot:        captureBot,
		Bridge:     b.store.client.UserLogin.Bridge,
		Portal:     portal,
		Processor:  nil,
		RoomID:     portal.MXID,
		OrigRoomID: portal.MXID,
		EventID:    eventID,
		User:       b.store.client.UserLogin.User,
		Command:    cmdName,
		Args:       args[1:],
		RawArgs:    rawArgs,
		ReplyTo:    "",
		Ctx:        runCtx,
		Log:        &logCopy,
		MessageStatus: &bridgev2.MessageStatus{
			Status: event.MessageStatusSuccess,
		},
	}

	handler.Run(ce)

	captureBot.WaitForMessages(runCtx, 5*time.Second, 500*time.Millisecond)
	message := captureBot.Messages()
	if message == "" {
		message = "Command dispatched. No immediate output was captured (the command may still respond asynchronously in-room)."
	}
	return message, nil
}

type captureMatrixAPI struct {
	bridgev2.MatrixAPI
	mu       sync.Mutex
	messages []string
}

func newCaptureMatrixAPI(api bridgev2.MatrixAPI) *captureMatrixAPI {
	return &captureMatrixAPI{
		MatrixAPI: api,
	}
}

func (c *captureMatrixAPI) SendMessage(ctx context.Context, roomID id.RoomID, eventType event.Type, content *event.Content, extra *bridgev2.MatrixSendExtra) (*mautrix.RespSendEvent, error) {
	c.captureContent(content)
	return c.MatrixAPI.SendMessage(ctx, roomID, eventType, content, extra)
}

func (c *captureMatrixAPI) captureContent(content *event.Content) {
	if content == nil {
		return
	}
	var body string
	if msg, ok := content.Parsed.(*event.MessageEventContent); ok {
		body = msg.Body
	} else if content.Raw != nil {
		if rawBody, ok := content.Raw["body"].(string); ok {
			body = rawBody
		}
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, body)
}

func (c *captureMatrixAPI) Messages() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimSpace(strings.Join(c.messages, "\n"))
}

func (c *captureMatrixAPI) messageCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.messages)
}

// WaitForMessages waits briefly for the first message, then extends the settle window
// while additional messages arrive.
func (c *captureMatrixAPI) WaitForMessages(ctx context.Context, firstTimeout, settleTimeout time.Duration) {
	if c == nil {
		return
	}
	if c.messageCount() == 0 && firstTimeout > 0 {
		firstDeadline := time.Now().Add(firstTimeout)
		for c.messageCount() == 0 {
			if time.Now().After(firstDeadline) {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	if settleTimeout <= 0 {
		return
	}

	lastCount := c.messageCount()
	settleDeadline := time.Now().Add(settleTimeout)
	for time.Now().Before(settleDeadline) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
			currentCount := c.messageCount()
			if currentCount != lastCount {
				lastCount = currentCount
				settleDeadline = time.Now().Add(settleTimeout)
			}
		}
	}
}

// CreateRoom implements tools.AgentStoreInterface.
func (b *BossStoreAdapter) CreateRoom(ctx context.Context, room tools.RoomData) (string, error) {
	// Get the agent to verify it exists
	agent, err := b.store.GetAgentByID(ctx, room.AgentID)
	if err != nil {
		return "", fmt.Errorf("agent '%s' not found: %w", room.AgentID, err)
	}

	// Create the portal via createAgentChat
	resp, err := b.store.client.createAgentChat(ctx, agent)
	if err != nil {
		return "", fmt.Errorf("failed to create room: %w", err)
	}

	// Get the portal to apply any overrides
	portal, err := b.store.client.UserLogin.Bridge.GetPortalByKey(ctx, resp.PortalKey)
	if err != nil {
		return "", fmt.Errorf("failed to get created portal: %w", err)
	}

	// Apply custom name and system prompt if provided
	pm := portalMeta(portal)
	originalName := portal.Name
	originalNameSet := portal.NameSet
	originalTitle := pm.Title
	originalTitleGenerated := pm.TitleGenerated
	originalSystemPrompt := pm.SystemPrompt

	if room.Name != "" {
		pm.Title = room.Name
		portal.Name = room.Name
		portal.NameSet = true
		if resp.PortalInfo != nil {
			resp.PortalInfo.Name = &room.Name
		}
	}
	if room.SystemPrompt != "" {
		pm.SystemPrompt = room.SystemPrompt
		// Note: portal.Topic is NOT set to SystemPrompt - they are separate concepts
		// Topic is for display only, SystemPrompt is for LLM context
	}

	// Create the Matrix room
	if err := portal.CreateMatrixRoom(ctx, b.store.client.UserLogin, resp.PortalInfo); err != nil {
		cleanupPortal(ctx, b.store.client, portal, "failed to create Matrix room")
		return "", fmt.Errorf("failed to create Matrix room: %w", err)
	}

	// Send welcome message (excluded from LLM history)
	b.store.client.sendWelcomeMessage(ctx, portal)

	if room.Name != "" {
		if err := b.store.client.setRoomNameNoSave(ctx, portal, room.Name); err != nil {
			b.store.client.log.Warn().Err(err).Msg("Failed to set Matrix room name")
			portal.Name = originalName
			portal.NameSet = originalNameSet
			pm.Title = originalTitle
			pm.TitleGenerated = originalTitleGenerated
		}
	}
	if room.SystemPrompt != "" {
		if err := b.store.client.setRoomSystemPromptNoSave(ctx, portal, room.SystemPrompt); err != nil {
			b.store.client.log.Warn().Err(err).Msg("Failed to set room system prompt")
			pm.SystemPrompt = originalSystemPrompt
		}
	}

	if err := portal.Save(ctx); err != nil {
		return "", fmt.Errorf("failed to save room overrides: %w", err)
	}

	return string(portal.PortalKey.ID), nil
}

// ModifyRoom implements tools.AgentStoreInterface.
func (b *BossStoreAdapter) ModifyRoom(ctx context.Context, roomID string, updates tools.RoomData) error {
	portal, err := b.resolvePortalByRoomID(ctx, roomID)
	if err != nil {
		return err
	}

	pm := portalMeta(portal)

	// Apply updates
	if updates.Name != "" {
		portal.Name = updates.Name
		pm.Title = updates.Name
		portal.NameSet = true
	}
	if updates.AgentID != "" {
		// Verify agent exists
		agent, err := b.store.GetAgentByID(ctx, updates.AgentID)
		if err != nil {
			return fmt.Errorf("agent '%s' not found: %w", updates.AgentID, err)
		}
		pm.AgentID = agent.ID
		pm.Model = ""
		modelID := b.store.client.effectiveModel(pm)
		pm.Capabilities = getModelCapabilities(modelID, b.store.client.findModelInfo(modelID))
		portal.OtherUserID = agentUserID(agent.ID)
		agentName := b.store.client.resolveAgentDisplayName(ctx, agent)
		b.store.client.ensureAgentGhostDisplayName(ctx, agent.ID, modelID, agentName)
	}
	if updates.SystemPrompt != "" {
		pm.SystemPrompt = updates.SystemPrompt
		// Note: portal.Topic is NOT set to SystemPrompt - they are separate concepts
	}

	if updates.Name != "" && portal.MXID != "" {
		if err := b.store.client.setRoomName(ctx, portal, updates.Name); err != nil {
			b.store.client.log.Warn().Err(err).Msg("Failed to set Matrix room name")
		}
	}
	if updates.SystemPrompt != "" && portal.MXID != "" {
		if err := b.store.client.setRoomSystemPrompt(ctx, portal, updates.SystemPrompt); err != nil {
			b.store.client.log.Warn().Err(err).Msg("Failed to set room system prompt")
		}
	}

	return portal.Save(ctx)
}

// ListRooms implements tools.AgentStoreInterface.
func (b *BossStoreAdapter) ListRooms(ctx context.Context) ([]tools.RoomData, error) {
	portals, err := b.store.client.listAllChatPortals(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list rooms: %w", err)
	}

	var rooms []tools.RoomData
	for _, portal := range portals {
		pm := portalMeta(portal)
		name := portal.Name
		if name == "" {
			name = pm.Title
		}
		roomID := string(portal.PortalKey.ID)
		if portal.MXID != "" {
			roomID = portal.MXID.String()
		}
		rooms = append(rooms, tools.RoomData{
			ID:      roomID,
			Name:    name,
			AgentID: pm.AgentID,
		})
	}

	return rooms, nil
}

// Verify interface compliance
var _ tools.AgentStoreInterface = (*BossStoreAdapter)(nil)

// agentToToolsData converts an AgentDefinition to tools.AgentData.
func agentToToolsData(agent *agents.AgentDefinition) tools.AgentData {
	return tools.AgentData{
		ID:           agent.ID,
		Name:         agent.Name,
		Description:  agent.Description,
		Model:        agent.Model.Primary,
		SystemPrompt: agent.SystemPrompt,
		Tools:        agent.Tools.Clone(),
		Subagents:    subagentsToTools(agent.Subagents),
		Temperature:  agent.Temperature,
		IsPreset:     agent.IsPreset,
		CreatedAt:    agent.CreatedAt,
		UpdatedAt:    agent.UpdatedAt,
	}
}

// toolsDataToAgent converts tools.AgentData to an AgentDefinition.
func toolsDataToAgent(data tools.AgentData) *agents.AgentDefinition {
	return &agents.AgentDefinition{
		ID:          data.ID,
		Name:        data.Name,
		Description: data.Description,
		Model: agents.ModelConfig{
			Primary: data.Model,
		},
		SystemPrompt: data.SystemPrompt,
		Tools:        data.Tools.Clone(),
		Subagents:    subagentsFromTools(data.Subagents),
		Temperature:  data.Temperature,
		IsPreset:     data.IsPreset,
		CreatedAt:    data.CreatedAt,
		UpdatedAt:    data.UpdatedAt,
	}
}
