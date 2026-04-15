package sdk

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

// Conversation represents a chat room the agent is participating in.
type Conversation struct {
	ID    string
	Title string

	ctx    context.Context
	portal *bridgev2.Portal
	login  *bridgev2.UserLogin
	sender bridgev2.EventSender

	agent                *Agent
	agentCatalog         AgentCatalog
	roomFeatures         *RoomFeatures
	roomFeaturesOverride func(*Conversation) *RoomFeatures
	turnConfig           *TurnConfig
	store                *conversationStateStore
	approvalFlow         *ApprovalFlow[*pendingSDKApprovalData]
	providerIdentity     ProviderIdentity

	intentOverride func(context.Context) (bridgev2.MatrixAPI, error)
}

func newConversation(ctx context.Context, portal *bridgev2.Portal, login *bridgev2.UserLogin, sender bridgev2.EventSender) *Conversation {
	conv := &Conversation{
		ctx:              ctx,
		portal:           portal,
		login:            login,
		sender:           sender,
		providerIdentity: normalizedProviderIdentity(ProviderIdentity{}),
	}
	if portal != nil {
		conv.ID = string(portal.ID)
		conv.Title = portal.Name
	}
	return conv
}

func normalizedProviderIdentity(identity ProviderIdentity) ProviderIdentity {
	if identity.IDPrefix == "" {
		identity.IDPrefix = "sdk"
	}
	if identity.LogKey == "" {
		identity.LogKey = identity.IDPrefix + "_msg_id"
	}
	if identity.StatusNetwork == "" {
		identity.StatusNetwork = identity.IDPrefix
	}
	return identity
}

// NewConversationOptions configures optional parameters for NewConversation.
type NewConversationOptions struct {
	ApprovalFlow *ApprovalFlow[*pendingSDKApprovalData]
	StateStore   *conversationStateStore
}

// NewConversation creates an SDK conversation wrapper for provider bridges that
// want to drive SDK turns without using the default sdkClient implementation.
func NewConversation[SessionT SessionValue, ConfigDataT ConfigValue](ctx context.Context, login *bridgev2.UserLogin, portal *bridgev2.Portal, sender bridgev2.EventSender, cfg *Config[SessionT, ConfigDataT], session SessionT, opts ...NewConversationOptions) *Conversation {
	conv := newConversation(ctx, portal, login, sender)
	var options NewConversationOptions
	if len(opts) > 0 {
		options = opts[0]
	}
	conv.store = options.StateStore
	if conv.store == nil {
		conv.store = newConversationStateStore()
	}
	conv.approvalFlow = options.ApprovalFlow
	if cfg == nil {
		return conv
	}
	conv.providerIdentity = normalizedProviderIdentity(cfg.ProviderIdentity)
	conv.agent = cfg.Agent
	conv.agentCatalog = cfg.AgentCatalog
	conv.roomFeatures = cfg.RoomFeatures
	conv.turnConfig = cfg.TurnManagement
	if cfg.GetCapabilities != nil {
		conv.roomFeaturesOverride = func(conv *Conversation) *RoomFeatures {
			return cfg.GetCapabilities(session, conv)
		}
	}
	return conv
}

func (c *Conversation) getIntent(ctx context.Context) (bridgev2.MatrixAPI, error) {
	if c == nil {
		return nil, fmt.Errorf("conversation is nil")
	}
	if c.intentOverride != nil {
		return c.intentOverride(ctx)
	}
	if c.portal == nil || c.login == nil {
		return nil, fmt.Errorf("no portal or login")
	}
	intent, ok := c.portal.GetIntentFor(ctx, c.sender, c.login, bridgev2.RemoteEventMessage)
	if !ok || intent == nil {
		return nil, fmt.Errorf("failed to get intent")
	}
	return intent, nil
}

func (c *Conversation) stateStore() *conversationStateStore {
	if c == nil {
		return nil
	}
	return c.store
}

func (c *Conversation) state() *sdkConversationState {
	if c == nil {
		return &sdkConversationState{}
	}
	return loadConversationState(c.portal, c.stateStore())
}

func (c *Conversation) saveState(ctx context.Context, state *sdkConversationState) error {
	if c == nil {
		return nil
	}
	return saveConversationState(ctx, c.portal, c.stateStore(), state)
}

func (c *Conversation) resolveDefaultAgent(ctx context.Context) (*Agent, error) {
	if c == nil {
		return nil, nil
	}
	for _, agentID := range c.state().RoomAgents.AgentIDs {
		if agent, err := c.resolveAgentByIdentifier(ctx, agentID); err == nil && agent != nil {
			return agent, nil
		}
	}
	if agent := c.agent; agent != nil {
		return agent, nil
	}
	if catalog := c.agentCatalog; catalog != nil {
		return catalog.DefaultAgent(ctx, c.login)
	}
	return nil, nil
}

func (c *Conversation) resolveAgentByIdentifier(ctx context.Context, identifier string) (*Agent, error) {
	if c == nil || strings.TrimSpace(identifier) == "" {
		return nil, nil
	}
	if agent := c.agent; agent != nil && agent.ID == identifier {
		return agent, nil
	}
	if catalog := c.agentCatalog; catalog != nil {
		return catalog.ResolveAgent(ctx, c.login, identifier)
	}
	return nil, nil
}

func (c *Conversation) currentRoomFeatures(ctx context.Context) *RoomFeatures {
	if c == nil {
		return nil
	}
	if c.roomFeaturesOverride != nil {
		if rf := c.roomFeaturesOverride(c); rf != nil {
			return rf
		}
	}
	if c.roomFeatures != nil {
		return c.roomFeatures
	}
	state := c.state()
	agents := make([]*Agent, 0, len(state.RoomAgents.AgentIDs))
	for _, agentID := range state.RoomAgents.AgentIDs {
		agent, err := c.resolveAgentByIdentifier(ctx, agentID)
		if err != nil || agent == nil {
			continue
		}
		agents = append(agents, agent)
	}
	if len(agents) == 0 {
		if defaultAgent, err := c.resolveDefaultAgent(ctx); err == nil && defaultAgent != nil {
			agents = append(agents, defaultAgent)
		}
	}
	if len(agents) == 0 {
		return defaultSDKFeatureConfig()
	}
	return computeRoomFeaturesForAgents(agents)
}

// Stream starts a new streaming response in this conversation.
func (c *Conversation) Stream(ctx context.Context) *Turn {
	return newTurn(ctx, c, nil, nil)
}

// StartTurn creates a new Turn for this conversation.
func (c *Conversation) StartTurn(ctx context.Context, agent *Agent, source *SourceRef) *Turn {
	return newTurn(ctx, c, agent, source)
}

// Context returns the conversation's context.
func (c *Conversation) Context() context.Context {
	return c.ctx
}

// Spec returns the current persisted conversation spec snapshot.
func (c *Conversation) Spec() ConversationSpec {
	state := c.state()
	return ConversationSpec{
		PortalID:             c.ID,
		Kind:                 state.Kind,
		Visibility:           state.Visibility,
		ParentConversationID: state.ParentConversationID,
		ParentEventID:        state.ParentEventID,
		Title:                c.Title,
		ArchiveOnCompletion:  state.ArchiveOnCompletion,
		Metadata:             maps.Clone(state.Metadata),
	}
}

// EnsureRoomAgent ensures the agent is part of the room agent set.
func (c *Conversation) EnsureRoomAgent(ctx context.Context, agent *Agent) error {
	if c == nil || agent == nil {
		return nil
	}
	if c.login != nil && c.login.Bridge != nil {
		ghost, err := c.login.Bridge.GetGhostByID(ctx, networkid.UserID(agent.ID))
		if err != nil {
			return err
		}
		if ghost != nil {
			ghost.UpdateInfo(ctx, agent.UserInfo())
		}
	}
	state := c.state()
	state.RoomAgents.AgentIDs = append(state.RoomAgents.AgentIDs, agent.ID)
	state.RoomAgents.AgentIDs = normalizeAgentIDs(state.RoomAgents.AgentIDs)
	if err := c.saveState(ctx, state); err != nil {
		return err
	}
	if c.portal != nil && c.login != nil {
		c.portal.UpdateCapabilities(ctx, c.login, false)
	}
	return nil
}

// RoomAgents returns the current room agent set.
func (c *Conversation) RoomAgents(ctx context.Context) (*RoomAgentSet, error) {
	state := c.state()
	if len(state.RoomAgents.AgentIDs) == 0 {
		defaultAgent, err := c.resolveDefaultAgent(ctx)
		if err != nil {
			return nil, err
		}
		if defaultAgent != nil {
			state.RoomAgents.AgentIDs = []string{defaultAgent.ID}
			_ = c.saveState(ctx, state)
		}
	}
	result := state.RoomAgents
	result.AgentIDs = slices.Clone(result.AgentIDs)
	return &result, nil
}

// Portal returns the underlying bridgev2.Portal.
func (c *Conversation) Portal() *bridgev2.Portal { return c.portal }

// Login returns the underlying bridgev2.UserLogin.
func (c *Conversation) Login() *bridgev2.UserLogin { return c.login }

// Sender returns the event sender for this conversation.
func (c *Conversation) Sender() bridgev2.EventSender { return c.sender }
