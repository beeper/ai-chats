package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/shared/stringutil"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"
)

func modelMatchesQuery(query string, model *ModelInfo) bool {
	if query == "" || model == nil {
		return false
	}
	if strings.Contains(strings.ToLower(model.ID), query) {
		return true
	}
	if strings.Contains(strings.ToLower(modelContactName(model.ID, model)), query) {
		return true
	}
	for _, ident := range modelContactIdentifiers(model.ID, model) {
		if strings.Contains(strings.ToLower(ident), query) {
			return true
		}
	}
	return false
}

func agentContactIdentifiers(agentID, modelID string, info *ModelInfo) []string {
	identifiers := []string{}
	agentID = strings.TrimSpace(agentID)
	if agentID != "" {
		identifiers = append(identifiers, agentID)
	}
	identifiers = append(identifiers, modelContactIdentifiers(modelID, info)...)
	return stringutil.DedupeStrings(identifiers)
}

// SearchUsers searches available AI models and agents by name/ID.
func (oc *AIClient) SearchUsers(ctx context.Context, query string) ([]*bridgev2.ResolveIdentifierResponse, error) {
	oc.loggerForContext(ctx).Debug().Str("query", query).Msg("Model/agent search requested")
	if !oc.IsLoggedIn() {
		return nil, mautrix.MForbidden.WithMessage("You must be logged in to search contacts")
	}

	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, nil
	}

	// Load agents
	store := NewAgentStoreAdapter(oc)
	agentsMap, err := store.LoadAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load agents: %w", err)
	}

	// Filter agents by query (match ID, name, or description)
	var results []*bridgev2.ResolveIdentifierResponse
	seen := make(map[networkid.UserID]struct{})
	for _, agent := range agentsMap {
		agentName := oc.resolveAgentDisplayName(ctx, agent)
		// Check if query matches agent ID, name, or description (case-insensitive)
		if !strings.Contains(strings.ToLower(agent.ID), query) &&
			!strings.Contains(strings.ToLower(agentName), query) &&
			!strings.Contains(strings.ToLower(agent.Description), query) {
			continue
		}

		modelID := oc.agentDefaultModel(agent)
		userID := agentUserID(agent.ID)
		ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, userID)
		if err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Str("agent", agent.ID).Msg("Failed to get ghost for search result")
			continue
		}

		displayName := agentName
		oc.ensureAgentGhostDisplayName(ctx, agent.ID, modelID, agentName)

		results = append(results, &bridgev2.ResolveIdentifierResponse{
			UserID: userID,
			UserInfo: &bridgev2.UserInfo{
				Name:        ptr.Ptr(displayName),
				IsBot:       ptr.Ptr(true),
				Identifiers: agentContactIdentifiers(agent.ID, modelID, oc.findModelInfo(modelID)),
			},
			Ghost: ghost,
		})
		seen[userID] = struct{}{}
	}

	// Filter models by query (match ID, display name, aliases, provider URIs)
	models, err := oc.listAvailableModels(ctx, false)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to load models for search")
	} else {
		for i := range models {
			model := &models[i]
			if model.ID == "" || !modelMatchesQuery(query, model) {
				continue
			}
			userID := modelUserID(model.ID)
			if _, ok := seen[userID]; ok {
				continue
			}
			ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, userID)
			if err != nil {
				oc.loggerForContext(ctx).Warn().Err(err).Str("model", model.ID).Msg("Failed to get ghost for model search result")
				continue
			}
			oc.ensureGhostDisplayNameWithGhost(ctx, ghost, model.ID, model)
			results = append(results, &bridgev2.ResolveIdentifierResponse{
				UserID: userID,
				UserInfo: &bridgev2.UserInfo{
					Name:        ptr.Ptr(modelContactName(model.ID, model)),
					IsBot:       ptr.Ptr(false),
					Identifiers: modelContactIdentifiers(model.ID, model),
				},
				Ghost: ghost,
			})
			seen[userID] = struct{}{}
		}
	}

	oc.loggerForContext(ctx).Info().Str("query", query).Int("results", len(results)).Msg("Model/agent search completed")
	return results, nil
}

// GetContactList returns a list of available AI agents and models as contacts
func (oc *AIClient) GetContactList(ctx context.Context) ([]*bridgev2.ResolveIdentifierResponse, error) {
	oc.loggerForContext(ctx).Debug().Msg("Contact list requested")
	if !oc.IsLoggedIn() {
		return nil, mautrix.MForbidden.WithMessage("You must be logged in to list contacts")
	}

	// Load agents
	store := NewAgentStoreAdapter(oc)
	agentsMap, err := store.LoadAgents(ctx)
	if err != nil {
		oc.loggerForContext(ctx).Error().Err(err).Msg("Failed to load agents")
		return nil, fmt.Errorf("failed to load agents: %w", err)
	}

	// Create a contact for each agent
	contacts := make([]*bridgev2.ResolveIdentifierResponse, 0, len(agentsMap))

	for _, agent := range agentsMap {
		// Get or create ghost for this agent
		modelID := oc.agentDefaultModel(agent)
		userID := agentUserID(agent.ID)
		ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, userID)
		if err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Str("agent", agent.ID).Msg("Failed to get ghost for agent")
			continue
		}

		// Update ghost display name
		agentName := oc.resolveAgentDisplayName(ctx, agent)
		displayName := agentName
		oc.ensureAgentGhostDisplayName(ctx, agent.ID, modelID, agentName)

		contacts = append(contacts, &bridgev2.ResolveIdentifierResponse{
			UserID: userID,
			UserInfo: &bridgev2.UserInfo{
				Name:        ptr.Ptr(displayName),
				IsBot:       ptr.Ptr(true),
				Identifiers: agentContactIdentifiers(agent.ID, modelID, oc.findModelInfo(modelID)),
			},
			Ghost: ghost,
		})
	}

	// Add contacts for available models
	models, err := oc.listAvailableModels(ctx, false)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to load model contact list")
	} else {
		for i := range models {
			model := &models[i]
			if model.ID == "" {
				continue
			}
			userID := modelUserID(model.ID)
			ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, userID)
			if err != nil {
				oc.loggerForContext(ctx).Warn().Err(err).Str("model", model.ID).Msg("Failed to get ghost for model")
				continue
			}

			// Ensure ghost display name is set before returning
			oc.ensureGhostDisplayNameWithGhost(ctx, ghost, model.ID, model)

			contacts = append(contacts, &bridgev2.ResolveIdentifierResponse{
				UserID: userID,
				UserInfo: &bridgev2.UserInfo{
					Name:        ptr.Ptr(modelContactName(model.ID, model)),
					IsBot:       ptr.Ptr(false),
					Identifiers: modelContactIdentifiers(model.ID, model),
				},
				Ghost: ghost,
			})
		}
	}

	oc.loggerForContext(ctx).Info().Int("count", len(contacts)).Msg("Returning contact list")
	return contacts, nil
}

// ResolveIdentifier resolves an agent ID to a ghost and optionally creates a chat.
func (oc *AIClient) ResolveIdentifier(ctx context.Context, identifier string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	id := strings.TrimSpace(identifier)
	if id == "" {
		return nil, bridgev2.WrapRespErr(errors.New("identifier is required"), mautrix.MInvalidParam)
	}

	store := NewAgentStoreAdapter(oc)

	// Check if identifier is a model ghost ID (model-{id}).
	if modelID := parseModelFromGhostID(id); modelID != "" {
		resolved, valid, err := oc.resolveModelID(ctx, modelID)
		if err != nil {
			return nil, err
		}
		if !valid || resolved == "" {
			return nil, bridgev2.WrapRespErr(fmt.Errorf("model '%s' not found", modelID), mautrix.MNotFound)
		}
		resp, err := oc.resolveModelIdentifier(ctx, resolved, createChat)
		if err != nil {
			return nil, err
		}
		if createChat && resp != nil && resp.Chat != nil {
			resp.Chat.DMRedirectedTo = modelRedirectTarget(modelID, resolved)
		}
		return resp, nil
	}

	// Check if identifier is an agent ghost ID (agent-{id})
	if agentID, ok := parseAgentFromGhostID(id); ok {
		agent, err := store.GetAgentByID(ctx, agentID)
		if err != nil || agent == nil {
			return nil, bridgev2.WrapRespErr(fmt.Errorf("agent '%s' not found", agentID), mautrix.MNotFound)
		}
		return oc.resolveAgentIdentifier(ctx, agent, createChat)
	}

	// Try to find as agent first (bare agent ID like "beeper", "boss")
	agent, err := store.GetAgentByID(ctx, id)
	if err == nil && agent != nil {
		return oc.resolveAgentIdentifier(ctx, agent, createChat)
	}

	// Allow explicit model aliases that resolve through configured catalog/aliases.
	resolved, valid, err := oc.resolveModelID(ctx, id)
	if err != nil {
		return nil, err
	}
	if valid && resolved != "" {
		resp, err := oc.resolveModelIdentifier(ctx, resolved, createChat)
		if err != nil {
			return nil, err
		}
		if createChat && resp != nil && resp.Chat != nil {
			resp.Chat.DMRedirectedTo = modelRedirectTarget(id, resolved)
		}
		return resp, nil
	}
	return nil, bridgev2.WrapRespErr(fmt.Errorf("identifier '%s' not found", id), mautrix.MNotFound)
}

// CreateChatWithGhost creates a DM for a known model or agent ghost.
func (oc *AIClient) CreateChatWithGhost(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.CreateChatResponse, error) {
	if ghost == nil {
		return nil, bridgev2.WrapRespErr(errors.New("ghost is required"), mautrix.MInvalidParam)
	}
	ghostID := string(ghost.ID)
	if modelID := parseModelFromGhostID(ghostID); modelID != "" {
		resolved, valid, err := oc.resolveModelID(ctx, modelID)
		if err != nil {
			return nil, err
		}
		if !valid || resolved == "" {
			return nil, bridgev2.WrapRespErr(fmt.Errorf("model '%s' not found", modelID), mautrix.MNotFound)
		}
		resp, err := oc.resolveModelIdentifier(ctx, resolved, true)
		if err != nil {
			return nil, err
		}
		if resp != nil && resp.Chat != nil {
			resp.Chat.DMRedirectedTo = modelRedirectTarget(modelID, resolved)
		}
		return resp.Chat, nil
	}
	if agentID, ok := parseAgentFromGhostID(ghostID); ok {
		store := NewAgentStoreAdapter(oc)
		agent, err := store.GetAgentByID(ctx, agentID)
		if err != nil || agent == nil {
			return nil, bridgev2.WrapRespErr(fmt.Errorf("agent '%s' not found", agentID), mautrix.MNotFound)
		}
		resp, err := oc.resolveAgentIdentifier(ctx, agent, true)
		if err != nil {
			return nil, err
		}
		return resp.Chat, nil
	}
	return nil, bridgev2.WrapRespErr(fmt.Errorf("unsupported ghost ID: %s", ghostID), mautrix.MInvalidParam)
}

// resolveAgentIdentifier resolves an agent to a ghost and optionally creates a chat
func (oc *AIClient) resolveAgentIdentifier(ctx context.Context, agent *agents.AgentDefinition, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	return oc.resolveAgentIdentifierWithModel(ctx, agent, "", createChat)
}

func (oc *AIClient) resolveAgentIdentifierWithModel(ctx context.Context, agent *agents.AgentDefinition, modelID string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	explicitModel := modelID != ""
	if modelID == "" {
		modelID = oc.agentDefaultModel(agent)
	}
	userID := agentUserID(agent.ID)
	ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get ghost: %w", err)
	}

	agentName := oc.resolveAgentDisplayName(ctx, agent)
	displayName := agentName
	oc.ensureAgentGhostDisplayName(ctx, agent.ID, modelID, agentName)

	var chatResp *bridgev2.CreateChatResponse
	if createChat {
		oc.loggerForContext(ctx).Info().Str("agent", agent.ID).Msg("Creating new chat for agent")
		chatResp, err = oc.createAgentChatWithModel(ctx, agent, modelID, explicitModel)
		if err != nil {
			return nil, fmt.Errorf("failed to create chat: %w", err)
		}
	}

	return &bridgev2.ResolveIdentifierResponse{
		UserID: userID,
		UserInfo: &bridgev2.UserInfo{
			Name:        ptr.Ptr(displayName),
			IsBot:       ptr.Ptr(true),
			Identifiers: agentContactIdentifiers(agent.ID, modelID, oc.findModelInfo(modelID)),
		},
		Ghost: ghost,
		Chat:  chatResp,
	}, nil
}

// resolveModelIdentifier resolves an explicit model alias/ID to a ghost.
func (oc *AIClient) resolveModelIdentifier(ctx context.Context, modelID string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	// Get or create ghost
	userID := modelUserID(modelID)
	ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get ghost: %w", err)
	}

	// Ensure ghost display name is set before returning
	oc.ensureGhostDisplayName(ctx, modelID)

	var chatResp *bridgev2.CreateChatResponse
	if createChat {
		oc.loggerForContext(ctx).Info().Str("model", modelID).Msg("Creating new chat for model")
		chatResp, err = oc.createNewChat(ctx, modelID)
		if err != nil {
			return nil, fmt.Errorf("failed to create chat: %w", err)
		}
	}

	info := oc.findModelInfo(modelID)
	return &bridgev2.ResolveIdentifierResponse{
		UserID: userID,
		UserInfo: &bridgev2.UserInfo{
			Name:        ptr.Ptr(modelContactName(modelID, info)),
			IsBot:       ptr.Ptr(false),
			Identifiers: modelContactIdentifiers(modelID, info),
		},
		Ghost: ghost,
		Chat:  chatResp,
	}, nil
}

// handleModelSwitch generates membership change events when switching models
// This creates leave/join events to show the model transition in the room timeline
// For agent rooms, it updates the agent ghost metadata.
func (oc *AIClient) handleModelSwitch(ctx context.Context, portal *bridgev2.Portal, oldModel, newModel string) {
	if oldModel == newModel || oldModel == "" || newModel == "" {
		return
	}

	meta := portalMeta(portal)
	agentID := resolveAgentID(meta)

	// Check if this is an agent room - update agent ghost metadata
	if agentID != "" {
		oc.handleAgentModelSwitch(ctx, portal, agentID, oldModel, newModel)
		return
	}

	// For non-agent rooms, use simple mode ghosts
	oc.loggerForContext(ctx).Info().
		Str("old_model", oldModel).
		Str("new_model", newModel).
		Stringer("portal", portal.PortalKey).
		Msg("Handling model switch")

	oldInfo := oc.findModelInfo(oldModel)
	newInfo := oc.findModelInfo(newModel)
	oldModelName := modelContactName(oldModel, oldInfo)
	newModelName := modelContactName(newModel, newInfo)

	// Pre-update the new model ghost's profile before queueing the event
	// This ensures the ghost has a display name set in its Matrix profile
	newGhost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, modelUserID(newModel))
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Str("model", newModel).Msg("Failed to get ghost for model switch")
	} else {
		oc.ensureGhostDisplayNameWithGhost(ctx, newGhost, newModel, newInfo)
	}

	// Create member changes: old model leaves, new model joins
	// Use MemberEventExtra to set displayname directly in the membership event
	// This works because MemberEventContent.Displayname has omitempty, so our Raw value is preserved
	memberChanges := &bridgev2.ChatMemberList{
		MemberMap: bridgev2.ChatMemberMap{
			modelUserID(oldModel): {
				EventSender: bridgev2.EventSender{
					Sender:      modelUserID(oldModel),
					SenderLogin: oc.UserLogin.ID,
				},
				Membership:     event.MembershipLeave,
				PrevMembership: event.MembershipJoin,
			},
			modelUserID(newModel): {
				EventSender: bridgev2.EventSender{
					Sender:      modelUserID(newModel),
					SenderLogin: oc.UserLogin.ID,
				},
				Membership: event.MembershipJoin,
				UserInfo: &bridgev2.UserInfo{
					Name:        ptr.Ptr(newModelName),
					IsBot:       ptr.Ptr(false),
					Identifiers: modelContactIdentifiers(newModel, newInfo),
				},
				MemberEventExtra: map[string]any{
					"displayname":            newModelName,
					"com.beeper.ai.model_id": newModel,
				},
			},
		},
	}

	// Update portal's OtherUserID to new model
	portal.OtherUserID = modelUserID(newModel)

	// Queue the ChatInfoChange event
	evt := &simplevent.ChatInfoChange{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventChatInfoChange,
			PortalKey: portal.PortalKey,
			Timestamp: time.Now(),
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Str("action", "model_switch").
					Str("old_model", oldModel).
					Str("new_model", newModel)
			},
		},
		ChatInfoChange: &bridgev2.ChatInfoChange{
			MemberChanges: memberChanges,
		},
	}

	oc.UserLogin.QueueRemoteEvent(evt)

	// Send a notice about the model change from the bridge bot
	notice := fmt.Sprintf("Switched from %s to %s", oldModelName, newModelName)
	oc.sendSystemNotice(ctx, portal, notice)

	// Update bridge info and capabilities to resend room features state event with new capabilities
	// This ensures the client knows what features the new model supports (vision, audio, etc.)
	portal.UpdateBridgeInfo(ctx)
	portal.UpdateCapabilities(ctx, oc.UserLogin, true)

	// Ensure only 1 AI ghost in room
	if err := oc.ensureSingleAIGhost(ctx, portal); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to ensure single AI ghost after model switch")
	}
}

// handleAgentModelSwitch handles model switching for agent rooms.
// Keeps a single agent ghost and updates member metadata.
func (oc *AIClient) handleAgentModelSwitch(ctx context.Context, portal *bridgev2.Portal, agentID, oldModel, newModel string) {
	// Get the agent to determine display name
	store := NewAgentStoreAdapter(oc)
	agent, err := store.GetAgentByID(ctx, agentID)
	if err != nil || agent == nil {
		oc.loggerForContext(ctx).Warn().Err(err).Str("agent", agentID).Msg("Agent not found for model switch")
		return
	}

	oc.loggerForContext(ctx).Info().
		Str("agent", agentID).
		Str("old_model", oldModel).
		Str("new_model", newModel).
		Stringer("portal", portal.PortalKey).
		Msg("Handling agent model switch")

	ghostID := agentUserID(agentID)
	agentName := oc.resolveAgentDisplayName(ctx, agent)
	displayName := agentName
	oldModelName := modelContactName(oldModel, oc.findModelInfo(oldModel))
	newModelName := modelContactName(newModel, oc.findModelInfo(newModel))
	oldGhostID := portal.OtherUserID

	// Update member metadata for the agent ghost
	memberMap := bridgev2.ChatMemberMap{
		ghostID: {
			EventSender: bridgev2.EventSender{
				Sender:      ghostID,
				SenderLogin: oc.UserLogin.ID,
			},
			Membership: event.MembershipJoin,
			UserInfo: &bridgev2.UserInfo{
				Name:        ptr.Ptr(displayName),
				IsBot:       ptr.Ptr(true),
				Identifiers: agentContactIdentifiers(agentID, newModel, oc.findModelInfo(newModel)),
			},
			MemberEventExtra: map[string]any{
				"displayname":            displayName,
				"com.beeper.ai.model_id": newModel,
				"com.beeper.ai.agent":    agentID,
			},
		},
	}
	if oldGhostID != "" && oldGhostID != ghostID {
		memberMap[oldGhostID] = bridgev2.ChatMember{
			EventSender: bridgev2.EventSender{
				Sender:      oldGhostID,
				SenderLogin: oc.UserLogin.ID,
			},
			Membership:     event.MembershipLeave,
			PrevMembership: event.MembershipJoin,
		}
	}
	memberChanges := &bridgev2.ChatMemberList{MemberMap: memberMap}

	// Update portal's OtherUserID to agent ghost
	portal.OtherUserID = ghostID
	oc.ensureAgentGhostDisplayName(ctx, agentID, newModel, agentName)

	// Queue the ChatInfoChange event
	evt := &simplevent.ChatInfoChange{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventChatInfoChange,
			PortalKey: portal.PortalKey,
			Timestamp: time.Now(),
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Str("action", "agent_model_switch").
					Str("agent", agentID).
					Str("old_model", oldModel).
					Str("new_model", newModel)
			},
		},
		ChatInfoChange: &bridgev2.ChatInfoChange{
			MemberChanges: memberChanges,
		},
	}

	oc.UserLogin.QueueRemoteEvent(evt)

	// Send a notice about the model change
	notice := fmt.Sprintf("Switched model from %s to %s", oldModelName, newModelName)
	oc.sendSystemNotice(ctx, portal, notice)

	// Update bridge info and capabilities
	portal.UpdateBridgeInfo(ctx)
	portal.UpdateCapabilities(ctx, oc.UserLogin, true)

	// Ensure only 1 AI ghost in room
	if err := oc.ensureSingleAIGhost(ctx, portal); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to ensure single AI ghost after agent model switch")
	}
}

// ensureSingleAIGhost ensures only 1 model/agent ghost is in the room at a time.
// Updates portal.OtherUserID if it doesn't match the expected ghost.
func (oc *AIClient) ensureSingleAIGhost(ctx context.Context, portal *bridgev2.Portal) error {
	meta := portalMeta(portal)

	// Determine which ghost SHOULD be in the room
	var expectedGhostID networkid.UserID
	agentID := resolveAgentID(meta)

	modelID := oc.effectiveModel(meta)
	if agentID != "" {
		expectedGhostID = agentUserID(agentID)
	} else {
		expectedGhostID = modelUserID(modelID)
	}

	// Update portal.OtherUserID if mismatched
	if portal.OtherUserID != expectedGhostID {
		oc.loggerForContext(ctx).Debug().
			Str("old_ghost", string(portal.OtherUserID)).
			Str("new_ghost", string(expectedGhostID)).
			Stringer("portal", portal.PortalKey).
			Msg("Updating portal OtherUserID to match expected ghost")
		portal.OtherUserID = expectedGhostID
		return portal.Save(ctx)
	}
	return nil
}
