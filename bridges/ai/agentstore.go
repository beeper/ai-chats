package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/pkg/agents"
	"github.com/beeper/agentremote/pkg/agents/agentconfig"
	"github.com/beeper/agentremote/pkg/agents/tools"
)

// AgentStoreAdapter implements agents.AgentStore with UserLogin metadata as source of truth.
type AgentStoreAdapter struct {
	client *AIClient
	mu     sync.RWMutex // protects custom agent metadata reads and writes
}

func NewAgentStoreAdapter(client *AIClient) *AgentStoreAdapter {
	return &AgentStoreAdapter{client: client}
}

// LoadAgents implements agents.AgentStore.
// It loads agents from presets and metadata-backed custom agents.
func (s *AgentStoreAdapter) LoadAgents(_ context.Context) (map[string]*agents.AgentDefinition, error) {
	// Start with preset agents
	result := make(map[string]*agents.AgentDefinition)

	// Resolve login metadata for provider gating
	loginMeta := loginMetadata(s.client.UserLogin)
	isMagicProxyProvider := loginMeta != nil && loginMeta.Provider == ProviderMagicProxy

	// Add all presets
	for _, preset := range agents.PresetAgents {
		if preset != nil && agents.IsBeeperHelp(preset.ID) && !isMagicProxyProvider {
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
	s.mu.RLock()
	defer s.mu.RUnlock()

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
	s.mu.RLock()
	defer s.mu.RUnlock()

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

func (s *AgentStoreAdapter) LoadBossAgents(ctx context.Context) (map[string]tools.AgentData, error) {
	agentsMap, err := s.LoadAgents(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]tools.AgentData, len(agentsMap))
	for id, agent := range agentsMap {
		result[id] = agentToToolsData(agent)
	}
	return result, nil
}

func (s *AgentStoreAdapter) SaveBossAgent(ctx context.Context, agent tools.AgentData) error {
	return s.SaveAgent(ctx, toolsDataToAgent(agent))
}

func (s *AgentStoreAdapter) ListBossModels(ctx context.Context) ([]tools.ModelData, error) {
	models, err := s.ListModels(ctx)
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
		Temperature:     ptr.Clone(agent.Temperature),
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

	content.MemorySearch = agent.MemorySearch

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
		Temperature:     ptr.Clone(content.Temperature),
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

	def.MemorySearch = content.MemorySearch

	return def
}

// BossStoreAdapter implements tools.AgentStoreInterface for boss tool execution.
// This adapter converts between our agent types and the tools package types.
type BossStoreAdapter struct {
	*AgentStoreAdapter
}

func NewBossStoreAdapter(client *AIClient) *BossStoreAdapter {
	return &BossStoreAdapter{
		AgentStoreAdapter: NewAgentStoreAdapter(client),
	}
}

// LoadAgents implements tools.AgentStoreInterface.
func (b *BossStoreAdapter) LoadAgents(ctx context.Context) (map[string]tools.AgentData, error) {
	return b.LoadBossAgents(ctx)
}

// SaveAgent implements tools.AgentStoreInterface.
func (b *BossStoreAdapter) SaveAgent(ctx context.Context, agent tools.AgentData) error {
	return b.SaveBossAgent(ctx, agent)
}

// DeleteAgent implements tools.AgentStoreInterface.
func (b *BossStoreAdapter) DeleteAgent(ctx context.Context, agentID string) error {
	return b.AgentStoreAdapter.DeleteAgent(ctx, agentID)
}

// ListModels implements tools.AgentStoreInterface.
func (b *BossStoreAdapter) ListModels(ctx context.Context) ([]tools.ModelData, error) {
	return b.ListBossModels(ctx)
}

// ListAvailableTools implements tools.AgentStoreInterface.
func (b *BossStoreAdapter) ListAvailableTools(ctx context.Context) ([]tools.ToolInfo, error) {
	return b.AgentStoreAdapter.ListAvailableTools(ctx)
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

	prefix := b.client.connector.br.Config.CommandPrefix
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

	runCtx := b.client.backgroundContext(ctx)
	logCopy := b.client.log.With().Str("mx_command", cmdName).Logger()
	captureBot := newCaptureMatrixAPI(b.client.UserLogin.Bridge.Bot)
	eventID := agentremote.NewEventID("internal")
	ce := &commands.Event{
		Bot:        captureBot,
		Bridge:     b.client.UserLogin.Bridge,
		Portal:     portal,
		Processor:  nil,
		RoomID:     portal.MXID,
		OrigRoomID: portal.MXID,
		EventID:    eventID,
		User:       b.client.UserLogin.User,
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
	agent, err := b.GetAgentByID(ctx, room.AgentID)
	if err != nil {
		return "", fmt.Errorf("agent '%s' not found: %w", room.AgentID, err)
	}

	// Create the portal via createAgentChatWithModel
	resp, err := b.client.createAgentChatWithModel(ctx, agent, "", false)
	if err != nil {
		return "", fmt.Errorf("failed to create room: %w", err)
	}

	// Get the portal to apply any overrides
	portal, err := b.client.UserLogin.Bridge.GetPortalByKey(ctx, resp.PortalKey)
	if err != nil {
		return "", fmt.Errorf("failed to get created portal: %w", err)
	}

	// Apply custom room name if provided.
	pm := portalMeta(portal)
	originalName := portal.Name
	originalNameSet := portal.NameSet
	originalTitle := pm.Title
	originalTitleGenerated := pm.TitleGenerated

	if room.Name != "" {
		pm.Title = room.Name
		portal.Name = room.Name
		portal.NameSet = true
		if resp.PortalInfo != nil {
			resp.PortalInfo.Name = &room.Name
		}
	}
	// Create the Matrix room
	if err := b.client.materializePortalRoom(ctx, portal, resp.PortalInfo, portalRoomMaterializeOptions{
		CleanupOnCreateError: "failed to create Matrix room",
		SendWelcome:          true,
	}); err != nil {
		return "", fmt.Errorf("failed to create Matrix room: %w", err)
	}

	if room.Name != "" {
		if err := b.client.setRoomName(ctx, portal, room.Name, false); err != nil {
			b.client.log.Warn().Err(err).Msg("Failed to set Matrix room name")
			portal.Name = originalName
			portal.NameSet = originalNameSet
			pm.Title = originalTitle
			pm.TitleGenerated = originalTitleGenerated
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
		agent, err := b.GetAgentByID(ctx, updates.AgentID)
		if err != nil {
			return fmt.Errorf("agent '%s' not found: %w", updates.AgentID, err)
		}
		portal.OtherUserID = b.client.agentUserID(agent.ID)
		pm.ResolvedTarget = resolveTargetFromGhostID(portal.OtherUserID)
		modelID := b.client.effectiveModel(pm)
		agentName := b.client.resolveAgentDisplayName(ctx, agent)
		b.client.ensureAgentGhostDisplayName(ctx, agent.ID, modelID, agentName)
	}

	if updates.Name != "" && portal.MXID != "" {
		if err := b.client.setRoomName(ctx, portal, updates.Name, true); err != nil {
			b.client.log.Warn().Err(err).Msg("Failed to set Matrix room name")
		}
	}

	return portal.Save(ctx)
}

// ListRooms implements tools.AgentStoreInterface.
func (b *BossStoreAdapter) ListRooms(ctx context.Context) ([]tools.RoomData, error) {
	portals, err := b.client.listAllChatPortals(ctx)
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
			AgentID: resolveAgentID(pm),
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
		Subagents:    agentconfig.CloneSubagentConfig(agent.Subagents),
		Temperature:  ptr.Clone(agent.Temperature),
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
		Subagents:    agentconfig.CloneSubagentConfig(data.Subagents),
		Temperature:  ptr.Clone(data.Temperature),
		IsPreset:     data.IsPreset,
		CreatedAt:    data.CreatedAt,
		UpdatedAt:    data.UpdatedAt,
	}
}
