package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/google/uuid"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/agents/tools"
)

// AgentStoreAdapter implements agents.AgentStore using Matrix state events.
type AgentStoreAdapter struct {
	client *AIClient
	mu     sync.Mutex // protects read-modify-write operations on custom agents
}

// NewAgentStoreAdapter creates a new agent store adapter.
func NewAgentStoreAdapter(client *AIClient) *AgentStoreAdapter {
	return &AgentStoreAdapter{client: client}
}

// LoadAgents implements agents.AgentStore.
// It loads agents from preset definitions and per-agent hidden data rooms.
func (s *AgentStoreAdapter) LoadAgents(ctx context.Context) (map[string]*agents.AgentDefinition, error) {
	// Start with preset agents
	result := make(map[string]*agents.AgentDefinition)

	// Add all presets
	for _, preset := range agents.PresetAgents {
		result[preset.ID] = preset.Clone()
	}

	// Add boss agent
	result[agents.BossAgent.ID] = agents.BossAgent.Clone()

	customContents := make(map[string]*AgentDefinitionContent)

	// Load custom agents from the Builder room (if available).
	builderAgents, err := s.loadCustomAgentsFromBuilderRoom(ctx)
	if err != nil {
		s.client.log.Warn().Err(err).Msg("Failed to load custom agents from builder room")
	} else {
		mergeAgentContents(customContents, builderAgents)
	}

	// Load custom agents from their individual hidden data rooms.
	customAgents, err := s.loadAllCustomAgents(ctx)
	if err != nil {
		s.client.log.Warn().Err(err).Msg("Failed to load custom agents from data rooms")
	} else {
		mergeAgentContents(customContents, customAgents)
	}

	for id, content := range customContents {
		result[id] = FromAgentDefinitionContent(content)
	}

	return result, nil
}

// loadAllCustomAgents scans for agent data rooms and loads their contents.
// Each custom agent has its own hidden room with a deterministic ID.
func (s *AgentStoreAdapter) loadAllCustomAgents(ctx context.Context) (map[string]*AgentDefinitionContent, error) {
	result := make(map[string]*AgentDefinitionContent)

	// Get all portals owned by this user login
	allDBPortals, err := s.client.UserLogin.Bridge.DB.Portal.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list portals: %w", err)
	}

	for _, dbPortal := range allDBPortals {
		// Filter to only portals owned by this user login
		if dbPortal.Receiver != s.client.UserLogin.ID {
			continue
		}

		// Check if this is an agent data room
		agentID, ok := parseAgentIDFromDataRoom(dbPortal.ID)
		if !ok {
			continue
		}

		// Load agent data from this room
		content, err := s.loadAgentFromDataRoom(ctx, agentID)
		if err != nil {
			s.client.log.Warn().Err(err).Str("agent_id", agentID).Msg("Failed to load agent from data room")
			continue
		}
		if content != nil {
			result[agentID] = content
		}
	}

	return result, nil
}

func (s *AgentStoreAdapter) loadCustomAgentsFromBuilderRoom(ctx context.Context) (map[string]*AgentDefinitionContent, error) {
	portal, err := s.builderRoomPortal(ctx)
	if err != nil || portal == nil || portal.MXID == "" {
		return nil, err
	}

	matrixConn := s.client.UserLogin.Bridge.Matrix
	stateConn, ok := matrixConn.(bridgev2.MatrixConnectorWithArbitraryRoomState)
	if !ok {
		return nil, fmt.Errorf("matrix connector does not support state access")
	}

	evt, err := stateConn.GetStateEvent(ctx, portal.MXID, CustomAgentsEventType, "")
	if err != nil || evt == nil {
		return nil, nil
	}

	var content CustomAgentsEventContent
	if err := json.Unmarshal(evt.Content.VeryRaw, &content); err != nil {
		return nil, fmt.Errorf("failed to parse custom agents event: %w", err)
	}

	if len(content.Agents) == 0 {
		return nil, nil
	}

	return content.Agents, nil
}

func mergeAgentContents(dst, src map[string]*AgentDefinitionContent) {
	if len(src) == 0 {
		return
	}
	for id, content := range src {
		if content == nil {
			continue
		}
		existing, ok := dst[id]
		if !ok || shouldReplaceAgentContent(existing, content) {
			dst[id] = content
		}
	}
}

func shouldReplaceAgentContent(existing, candidate *AgentDefinitionContent) bool {
	if existing == nil {
		return true
	}
	if candidate == nil {
		return false
	}
	if existing.UpdatedAt == 0 && candidate.UpdatedAt == 0 {
		return false
	}
	if existing.UpdatedAt == 0 {
		return true
	}
	if candidate.UpdatedAt == 0 {
		return false
	}
	return candidate.UpdatedAt >= existing.UpdatedAt
}

// getOrCreateAgentDataRoom returns or creates the hidden data room for an agent.
func (s *AgentStoreAdapter) getOrCreateAgentDataRoom(ctx context.Context, agentID string) (*bridgev2.Portal, error) {
	portalKey := agentDataPortalKey(s.client.UserLogin.ID, agentID)

	portal, err := s.client.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get portal: %w", err)
	}

	// Check if portal already exists with a Matrix room
	if portal.MXID != "" {
		return portal, nil
	}

	// Create the portal and Matrix room
	return s.createAgentDataRoom(ctx, agentID)
}

// createAgentDataRoom creates a hidden room for storing agent data.
func (s *AgentStoreAdapter) createAgentDataRoom(ctx context.Context, agentID string) (*bridgev2.Portal, error) {
	portalKey := agentDataPortalKey(s.client.UserLogin.ID, agentID)

	portal, err := s.client.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get portal: %w", err)
	}

	// Set up portal metadata for hidden agent data room
	portal.Metadata = &PortalMetadata{
		IsAgentDataRoom: true,
		AgentID:         agentID,
	}
	portal.Name = fmt.Sprintf("Agent Data: %s", agentID)
	portal.NameSet = true

	if err := portal.Save(ctx); err != nil {
		return nil, fmt.Errorf("failed to save portal: %w", err)
	}

	// Create the Matrix room (hidden from clients via BeeperRoomTypeV2)
	chatInfo := &bridgev2.ChatInfo{
		Name: &portal.Name,
	}
	if err := portal.CreateMatrixRoom(ctx, s.client.UserLogin, chatInfo); err != nil {
		return nil, fmt.Errorf("failed to create Matrix room: %w", err)
	}

	s.client.log.Info().Str("agent_id", agentID).Stringer("portal", portal.PortalKey).Msg("Created agent data room")
	return portal, nil
}

// loadAgentFromDataRoom loads agent definition from its hidden room.
func (s *AgentStoreAdapter) loadAgentFromDataRoom(ctx context.Context, agentID string) (*AgentDefinitionContent, error) {
	portalKey := agentDataPortalKey(s.client.UserLogin.ID, agentID)

	portal, err := s.client.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get portal: %w", err)
	}
	if portal.MXID == "" {
		return nil, nil // Room doesn't exist yet
	}

	// Get the Matrix connector to read state events
	matrixConn := s.client.UserLogin.Bridge.Matrix
	stateConn, ok := matrixConn.(bridgev2.MatrixConnectorWithArbitraryRoomState)
	if !ok {
		return nil, fmt.Errorf("matrix connector does not support state access")
	}

	// Read the agent data state event
	evt, err := stateConn.GetStateEvent(ctx, portal.MXID, AgentDataEventType, "")
	if err != nil {
		// State event doesn't exist yet - this is normal for newly created rooms
		return nil, nil
	}

	// Parse the content
	var content AgentDataEventContent
	if err := json.Unmarshal(evt.Content.VeryRaw, &content); err != nil {
		return nil, fmt.Errorf("failed to parse agent data event: %w", err)
	}

	return content.Agent, nil
}

// saveAgentToDataRoom saves agent definition to its hidden room.
func (s *AgentStoreAdapter) saveAgentToDataRoom(ctx context.Context, agent *AgentDefinitionContent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Get or create the agent's data room
	portal, err := s.getOrCreateAgentDataRoom(ctx, agent.ID)
	if err != nil {
		return fmt.Errorf("failed to get/create agent data room: %w", err)
	}

	// Send the agent data state event
	content := &AgentDataEventContent{
		Agent: agent,
	}

	bot := s.client.UserLogin.Bridge.Bot
	_, err = bot.SendState(ctx, portal.MXID, AgentDataEventType, "", &event.Content{
		Parsed: content,
	}, time.Time{})
	if err != nil {
		return fmt.Errorf("failed to send agent data state event: %w", err)
	}

	return nil
}

func (s *AgentStoreAdapter) saveAgentToBuilderRoom(ctx context.Context, agent *AgentDefinitionContent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	portal, err := s.builderRoomPortal(ctx)
	if err != nil || portal == nil || portal.MXID == "" {
		return err
	}

	matrixConn := s.client.UserLogin.Bridge.Matrix
	stateConn, ok := matrixConn.(bridgev2.MatrixConnectorWithArbitraryRoomState)
	if !ok {
		return fmt.Errorf("matrix connector does not support state access")
	}

	content := CustomAgentsEventContent{
		Agents: map[string]*AgentDefinitionContent{},
	}

	evt, err := stateConn.GetStateEvent(ctx, portal.MXID, CustomAgentsEventType, "")
	if err == nil && evt != nil {
		if err := json.Unmarshal(evt.Content.VeryRaw, &content); err != nil {
			return fmt.Errorf("failed to parse existing custom agents event: %w", err)
		}
	}

	if content.Agents == nil {
		content.Agents = map[string]*AgentDefinitionContent{}
	}
	content.Agents[agent.ID] = agent

	bot := s.client.UserLogin.Bridge.Bot
	_, err = bot.SendState(ctx, portal.MXID, CustomAgentsEventType, "", &event.Content{
		Parsed: &content,
	}, time.Time{})
	if err != nil {
		return fmt.Errorf("failed to send custom agents state event: %w", err)
	}
	return nil
}

func (s *AgentStoreAdapter) deleteAgentFromBuilderRoom(ctx context.Context, agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	portal, err := s.builderRoomPortal(ctx)
	if err != nil || portal == nil || portal.MXID == "" {
		return err
	}

	matrixConn := s.client.UserLogin.Bridge.Matrix
	stateConn, ok := matrixConn.(bridgev2.MatrixConnectorWithArbitraryRoomState)
	if !ok {
		return fmt.Errorf("matrix connector does not support state access")
	}

	evt, err := stateConn.GetStateEvent(ctx, portal.MXID, CustomAgentsEventType, "")
	if err != nil || evt == nil {
		return nil
	}

	var content CustomAgentsEventContent
	if err := json.Unmarshal(evt.Content.VeryRaw, &content); err != nil {
		return fmt.Errorf("failed to parse custom agents event: %w", err)
	}

	if len(content.Agents) == 0 {
		return nil
	}

	if _, ok := content.Agents[agentID]; !ok {
		return nil
	}
	delete(content.Agents, agentID)

	bot := s.client.UserLogin.Bridge.Bot
	_, err = bot.SendState(ctx, portal.MXID, CustomAgentsEventType, "", &event.Content{
		Parsed: &content,
	}, time.Time{})
	if err != nil {
		return fmt.Errorf("failed to send custom agents state event: %w", err)
	}
	return nil
}

func (s *AgentStoreAdapter) builderRoomPortal(ctx context.Context) (*bridgev2.Portal, error) {
	meta := loginMetadata(s.client.UserLogin)
	if meta == nil || meta.BuilderRoomID == "" {
		return nil, nil
	}

	portalKey := networkid.PortalKey{
		ID:       meta.BuilderRoomID,
		Receiver: s.client.UserLogin.ID,
	}
	portal, err := s.client.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return nil, err
	}
	if portal == nil || portal.MXID == "" {
		return nil, nil
	}
	return portal, nil
}

// deleteAgentDataRoom deletes the hidden data room for an agent.
func (s *AgentStoreAdapter) deleteAgentDataRoom(ctx context.Context, agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	portalKey := agentDataPortalKey(s.client.UserLogin.ID, agentID)

	portal, err := s.client.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return fmt.Errorf("failed to get portal: %w", err)
	}
	if portal == nil {
		return nil // Already doesn't exist
	}

	// Delete the Matrix room if it exists
	if portal.MXID != "" {
		if err := portal.Delete(ctx); err != nil {
			s.client.log.Warn().Err(err).Str("agent_id", agentID).Msg("Failed to delete agent data room")
			// Continue to delete the portal from database even if Matrix delete fails
		}
	}

	// Delete the portal from database
	if err := s.client.UserLogin.Bridge.DB.Portal.Delete(ctx, portalKey); err != nil {
		return fmt.Errorf("failed to delete portal from database: %w", err)
	}

	s.client.log.Info().Str("agent_id", agentID).Msg("Deleted agent data room")
	return nil
}

// SaveAgent implements agents.AgentStore.
// It saves an agent to its own hidden data room.
func (s *AgentStoreAdapter) SaveAgent(ctx context.Context, agent *agents.AgentDefinition) error {
	if err := agent.Validate(); err != nil {
		return err
	}
	if agent.IsPreset {
		return agents.ErrAgentIsPreset
	}

	content := ToAgentDefinitionContent(agent)

	errData := s.saveAgentToDataRoom(ctx, content)
	if errData != nil {
		s.client.log.Warn().Err(errData).Str("agent_id", agent.ID).Msg("Failed to save custom agent to data room")
	}

	errBuilder := s.saveAgentToBuilderRoom(ctx, content)
	if errBuilder != nil {
		s.client.log.Warn().Err(errBuilder).Str("agent_id", agent.ID).Msg("Failed to save custom agent to builder room")
	}

	if errData != nil && errBuilder != nil {
		return errData
	}

	if errData == nil {
		s.client.log.Info().Str("agent_id", agent.ID).Str("name", agent.Name).Msg("Saved custom agent to data room")
	} else {
		s.client.log.Info().Str("agent_id", agent.ID).Str("name", agent.Name).Msg("Saved custom agent to builder room")
	}
	return nil
}

// DeleteAgent implements agents.AgentStore.
// It deletes an agent and its hidden data room.
func (s *AgentStoreAdapter) DeleteAgent(ctx context.Context, agentID string) error {
	if agents.IsPreset(agentID) || agents.IsBossAgent(agentID) {
		return agents.ErrAgentIsPreset
	}

	// Check if agent exists in either storage
	content, err := s.loadAgentFromDataRoom(ctx, agentID)
	if err != nil {
		return fmt.Errorf("failed to check agent existence: %w", err)
	}
	builderAgents, err := s.loadCustomAgentsFromBuilderRoom(ctx)
	if err != nil {
		s.client.log.Warn().Err(err).Str("agent_id", agentID).Msg("Failed to check builder room for agent")
	}
	_, foundInBuilder := builderAgents[agentID]
	if content == nil && !foundInBuilder {
		return agents.ErrAgentNotFound
	}

	// Delete the agent's hidden data room
	errData := s.deleteAgentDataRoom(ctx, agentID)
	if errData != nil {
		s.client.log.Warn().Err(errData).Str("agent_id", agentID).Msg("Failed to delete agent data room")
	}
	errBuilder := s.deleteAgentFromBuilderRoom(ctx, agentID)
	if errBuilder != nil {
		s.client.log.Warn().Err(errBuilder).Str("agent_id", agentID).Msg("Failed to delete agent from builder room")
	}
	if errData != nil && errBuilder != nil {
		return errData
	}

	s.client.log.Info().Str("agent_id", agentID).Msg("Deleted custom agent and data room")
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

	// Convert memory config
	if agent.Memory != nil {
		content.MemoryConfig = &AgentMemoryConfig{
			Enabled:      agent.Memory.Enabled,
			Sources:      agent.Memory.Sources,
			EnableGlobal: agent.Memory.EnableGlobal,
			MaxResults:   agent.Memory.MaxResults,
			MinScore:     agent.Memory.MinScore,
		}
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

	// Restore memory config if present
	if content.MemoryConfig != nil {
		def.Memory = &agents.MemoryConfig{
			Enabled:      content.MemoryConfig.Enabled,
			Sources:      content.MemoryConfig.Sources,
			EnableGlobal: content.MemoryConfig.EnableGlobal,
			MaxResults:   content.MemoryConfig.MaxResults,
			MinScore:     content.MemoryConfig.MinScore,
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
		return "", fmt.Errorf("command is required")
	}
	if roomID == "" {
		return "", fmt.Errorf("room_id is required")
	}

	prefix := b.store.client.connector.br.Config.CommandPrefix
	if strings.HasPrefix(command, prefix) {
		command = strings.TrimSpace(strings.TrimPrefix(command, prefix))
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("command is empty after trimming prefix")
	}

	args := strings.Fields(command)
	if len(args) == 0 {
		return "", fmt.Errorf("command is empty")
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

	logCopy := b.store.client.log.With().Str("mx_command", cmdName).Logger()
	captureBot := &captureMatrixAPI{MatrixAPI: b.store.client.UserLogin.Bridge.Bot}
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
		Ctx:        ctx,
		Log:        &logCopy,
		MessageStatus: &bridgev2.MessageStatus{
			Status: event.MessageStatusSuccess,
		},
	}

	handler.Run(ce)

	message := captureBot.Messages()
	if message == "" {
		message = "Command dispatched."
	}
	return message, nil
}

type captureMatrixAPI struct {
	bridgev2.MatrixAPI
	mu       sync.Mutex
	messages []string
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
		pm.DefaultAgentID = agent.ID
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
