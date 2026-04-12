package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/beeper/agentremote/pkg/agents"
	"github.com/beeper/agentremote/pkg/agents/tools"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/pkg/shared/toolspec"
	"github.com/beeper/agentremote/sdk"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Tool name constants
const (
	ToolNameCalculator = toolspec.CalculatorName
	ToolNameWebSearch  = toolspec.WebSearchName
)

func hasAssignedAgent(meta *PortalMetadata) bool {
	return resolveAgentID(meta) != ""
}

func hasBossAgent(meta *PortalMetadata) bool {
	return agents.IsBossAgent(resolveAgentID(meta))
}

func modelRedirectTarget(requested, resolved string) networkid.UserID {
	requested = strings.TrimSpace(requested)
	resolved = strings.TrimSpace(resolved)
	if requested == "" || resolved == "" || requested == resolved {
		return ""
	}
	return modelUserID(resolved)
}

func (oc *AIClient) agentsEnabledForLogin() bool {
	if oc == nil || oc.UserLogin == nil {
		return false
	}
	return agentsEnabled(loginMetadata(oc.UserLogin))
}

func shouldEnsureDefaultChat(meta *UserLoginMetadata) bool {
	if meta == nil {
		return false
	}
	return meta.Agents == nil || *meta.Agents
}

func agentChatsDisabledError() error {
	return bridgev2.WrapRespErr(errors.New("agent chats are disabled for this login"), mautrix.MForbidden)
}

// buildAvailableTools returns a list of ToolInfo for all tools based on tool policy.
func (oc *AIClient) buildAvailableTools(meta *PortalMetadata) []ToolInfo {
	names := oc.toolNamesForPortal(meta)
	var toolsList []ToolInfo

	for _, name := range names {
		metaTool := tools.GetTool(name)
		displayName := name
		description := ""
		toolType := "builtin"
		if metaTool != nil {
			description = metaTool.Description
			if metaTool.Annotations != nil && metaTool.Annotations.Title != "" {
				displayName = metaTool.Annotations.Title
			}
			if metaTool.Type != "" {
				toolType = string(metaTool.Type)
			}
		} else if oc != nil {
			lookupCtx, cancel := context.WithTimeout(context.Background(), mcpDiscoveryTimeout)
			if dynamicTool, ok := oc.lookupMCPToolDefinition(lookupCtx, name); ok {
				description = dynamicTool.Description
				toolType = string(ToolTypeMCP)
			}
			cancel()
		}
		description = oc.toolDescriptionForPortal(meta, name, description)

		available, source, reason := oc.isToolAvailable(meta, name)
		allowed := oc.isToolAllowedByPolicy(meta, name)
		enabled := available && allowed

		if !allowed {
			source = SourceAgentPolicy
			if reason == "" {
				reason = "Disabled by tool policy"
			}
		}

		toolsList = append(toolsList, ToolInfo{
			Name:        name,
			DisplayName: displayName,
			Description: description,
			Type:        toolType,
			Enabled:     enabled,
			Available:   available,
			Source:      source,
			Reason:      reason,
		})
	}

	return toolsList
}

func (oc *AIClient) canUseImageGeneration() bool {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Metadata == nil {
		return false
	}
	loginMeta := loginMetadata(oc.UserLogin)
	if loginMeta == nil || strings.TrimSpace(oc.connector.resolveProviderAPIKey(loginMeta)) == "" {
		return false
	}
	switch loginMeta.Provider {
	case ProviderOpenAI, ProviderOpenRouter, ProviderMagicProxy:
		return true
	default:
		return false
	}
}

func normalizeModelSearchString(s string) string {
	replacer := strings.NewReplacer("-", " ", "_", " ", ".", " ", "/", " ", ":", " ")
	return strings.Join(strings.Fields(replacer.Replace(strings.ToLower(strings.TrimSpace(s)))), " ")
}

func modelMatchesQuery(query string, model *ModelInfo) bool {
	if query == "" || model == nil {
		return false
	}
	rawQuery := strings.ToLower(strings.TrimSpace(query))
	if rawQuery == "" {
		return false
	}
	normalizedQuery := normalizeModelSearchString(rawQuery)

	if strings.Contains(strings.ToLower(model.ID), rawQuery) ||
		(normalizedQuery != "" && strings.Contains(normalizeModelSearchString(model.ID), normalizedQuery)) {
		return true
	}
	name := modelContactName(model.ID, model)
	if strings.Contains(strings.ToLower(name), rawQuery) ||
		(normalizedQuery != "" && strings.Contains(normalizeModelSearchString(name), normalizedQuery)) {
		return true
	}
	if provider := modelContactProvider(model.ID, model); provider != "" && name != "" {
		providerAlias := provider + "/" + name
		if strings.Contains(strings.ToLower(providerAlias), rawQuery) ||
			(normalizedQuery != "" && strings.Contains(normalizeModelSearchString(providerAlias), normalizedQuery)) {
			return true
		}
	}
	if openRouterURL := modelContactOpenRouterURL(model.ID, model); openRouterURL != "" {
		if strings.Contains(strings.ToLower(openRouterURL), rawQuery) ||
			(normalizedQuery != "" && strings.Contains(normalizeModelSearchString(openRouterURL), normalizedQuery)) {
			return true
		}
	}
	for _, ident := range modelContactIdentifiers(model.ID) {
		if strings.Contains(strings.ToLower(ident), rawQuery) ||
			(normalizedQuery != "" && strings.Contains(normalizeModelSearchString(ident), normalizedQuery)) {
			return true
		}
	}
	return false
}

func agentContactIdentifiers(agentID string) []string {
	var identifiers []string
	if ident := canonicalAgentIdentifier(agentID); ident != "" {
		identifiers = append(identifiers, ident)
	}
	return stringutil.DedupeStrings(identifiers)
}

func agentMatchesQuery(query string, agent *sdk.Agent) bool {
	if query == "" || agent == nil {
		return false
	}
	matches := []string{agent.ID, agent.Name, agent.Description}
	matches = append(matches, agent.Identifiers...)
	for _, candidate := range matches {
		if strings.Contains(strings.ToLower(strings.TrimSpace(candidate)), query) {
			return true
		}
	}
	return false
}

func (oc *AIClient) modelContactResponse(ctx context.Context, model *ModelInfo) *bridgev2.ResolveIdentifierResponse {
	if model == nil || model.ID == "" {
		return nil
	}
	responder, err := oc.ResolveResponderForModel(ctx, model.ID)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Str("model", model.ID).Msg("Failed to resolve responder for model contact")
	}
	resp := &bridgev2.ResolveIdentifierResponse{
		UserID:   modelUserID(model.ID),
		UserInfo: responderUserInfoOrDefault(responder, modelContactName(model.ID, model), modelContactIdentifiers(model.ID), false),
	}
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return resp
	}
	ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, resp.UserID)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Str("model", model.ID).Msg("Failed to hydrate ghost for model contact")
		return resp
	}
	resp.Ghost = ghost
	return resp
}

func (oc *AIClient) agentContactResponse(ctx context.Context, agent *sdk.Agent) *bridgev2.ResolveIdentifierResponse {
	if agent == nil || !oc.agentsEnabledForLogin() {
		return nil
	}
	resp := &bridgev2.ResolveIdentifierResponse{
		UserID: networkid.UserID(agent.ID),
	}
	if agentInfo := agent.UserInfo(); agentInfo != nil {
		resp.UserInfo = agentInfo
	}
	if agentID := catalogAgentID(agent); agentID != "" {
		responder, err := oc.ResolveResponderForAgent(ctx, agentID, ResponderResolveOptions{})
		if err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Str("agent", agentID).Msg("Failed to resolve responder for agent contact")
		} else if resp.UserInfo == nil {
			resp.UserInfo = responderUserInfo(responder, agent.Identifiers, true)
		} else {
			resp.UserInfo.ExtraProfile = responderExtraProfile(responder)
		}
	}
	if resp.UserInfo == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || resp.UserID == "" {
		return resp
	}
	ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, resp.UserID)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Str("agent", string(resp.UserID)).Msg("Failed to hydrate ghost for agent contact")
		return resp
	}
	resp.Ghost = ghost
	return resp
}

func catalogAgentID(agent *sdk.Agent) string {
	if agent == nil {
		return ""
	}
	if agentID, ok := parseAgentFromGhostID(strings.TrimSpace(agent.ID)); ok {
		return agentID
	}
	for _, identifier := range agent.Identifiers {
		if agentID, ok := parseAgentFromGhostID(strings.TrimSpace(identifier)); ok {
			return agentID
		}
		if normalized := normalizeAgentID(identifier); normalized != "" {
			return normalized
		}
	}
	return ""
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

	agentsList, err := oc.sdkAgentCatalog().ListAgents(ctx, oc.UserLogin)
	if err != nil {
		return nil, fmt.Errorf("failed to load agents: %w", err)
	}

	var results []*bridgev2.ResolveIdentifierResponse
	seen := make(map[networkid.UserID]struct{})
	for _, agent := range agentsList {
		if !agentMatchesQuery(query, agent) {
			continue
		}
		resp := oc.agentContactResponse(ctx, agent)
		if resp == nil {
			continue
		}
		results = append(results, resp)
		seen[resp.UserID] = struct{}{}
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
			resp := oc.modelContactResponse(ctx, model)
			if resp == nil {
				continue
			}
			if _, ok := seen[resp.UserID]; ok {
				continue
			}
			results = append(results, resp)
			seen[resp.UserID] = struct{}{}
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

	agentsList, err := oc.sdkAgentCatalog().ListAgents(ctx, oc.UserLogin)
	if err != nil {
		oc.loggerForContext(ctx).Error().Err(err).Msg("Failed to load agents")
		return nil, fmt.Errorf("failed to load agents: %w", err)
	}

	contacts := make([]*bridgev2.ResolveIdentifierResponse, 0, len(agentsList))
	for _, agent := range agentsList {
		if resp := oc.agentContactResponse(ctx, agent); resp != nil {
			contacts = append(contacts, resp)
		}
	}

	// Add contacts for available models
	models, err := oc.listAvailableModels(ctx, false)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to load model contact list")
	} else {
		for i := range models {
			model := &models[i]
			if resp := oc.modelContactResponse(ctx, model); resp != nil {
				contacts = append(contacts, resp)
			}
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

	if canonicalModelID := parseCanonicalModelIdentifier(id); canonicalModelID != "" {
		id = canonicalModelID
	} else if canonicalAgentID := parseCanonicalAgentIdentifier(id); canonicalAgentID != "" {
		id = canonicalAgentID
	}

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

	if catalogAgent, err := oc.sdkAgentCatalog().ResolveAgent(ctx, oc.UserLogin, id); err == nil && catalogAgent != nil {
		agentID := catalogAgentID(catalogAgent)
		if agentID == "" {
			if resp := oc.agentContactResponse(ctx, catalogAgent); resp != nil {
				return resp, nil
			}
			return nil, bridgev2.WrapRespErr(fmt.Errorf("agent '%s' not found", id), mautrix.MNotFound)
		}
		agent, resolveErr := NewAgentStoreAdapter(oc).GetAgentByID(ctx, agentID)
		if resolveErr == nil && agent != nil {
			return oc.resolveAgentIdentifier(ctx, agent, "", createChat)
		}
		return nil, bridgev2.WrapRespErr(fmt.Errorf("agent '%s' not found", agentID), mautrix.MNotFound)
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
		if !oc.agentsEnabledForLogin() {
			return nil, agentChatsDisabledError()
		}
		store := NewAgentStoreAdapter(oc)
		agent, err := store.GetAgentByID(ctx, agentID)
		if err != nil || agent == nil {
			return nil, bridgev2.WrapRespErr(fmt.Errorf("agent '%s' not found", agentID), mautrix.MNotFound)
		}
		resp, err := oc.resolveAgentIdentifier(ctx, agent, "", true)
		if err != nil {
			return nil, err
		}
		return resp.Chat, nil
	}
	return nil, bridgev2.WrapRespErr(fmt.Errorf("unsupported ghost ID: %s", ghostID), mautrix.MInvalidParam)
}

// resolveAgentIdentifier resolves an agent to a ghost and optionally creates a chat.
func (oc *AIClient) resolveAgentIdentifier(ctx context.Context, agent *agents.AgentDefinition, modelID string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	if !oc.agentsEnabledForLogin() {
		return nil, agentChatsDisabledError()
	}
	explicitModel := modelID != ""
	if modelID == "" {
		modelID = oc.agentDefaultModel(agent)
	}
	userID := oc.agentUserID(agent.ID)
	var ghost *bridgev2.Ghost
	if oc != nil && oc.UserLogin != nil && oc.UserLogin.Bridge != nil {
		var err error
		ghost, err = oc.UserLogin.Bridge.GetGhostByID(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("failed to get ghost: %w", err)
		}
	}

	agentName := oc.resolveAgentDisplayName(ctx, agent)
	if agentName == "" {
		agentName = strings.TrimSpace(agent.EffectiveName())
	}
	if agentName == "" {
		agentName = agent.ID
	}
	oc.ensureAgentGhostDisplayName(ctx, agent.ID, modelID, agentName)
	responder, err := oc.ResolveResponderForAgent(ctx, agent.ID, ResponderResolveOptions{
		RuntimeModelOverride: modelID,
	})
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Str("agent", agent.ID).Msg("Failed to resolve responder for agent identifier")
		responder = nil
	}

	var chatResp *bridgev2.CreateChatResponse
	if createChat {
		oc.loggerForContext(ctx).Info().Str("agent", agent.ID).Msg("Creating new chat for agent")
		chatResp, err = oc.createAgentChatWithModel(ctx, agent, modelID, explicitModel)
		if err != nil {
			return nil, fmt.Errorf("failed to create chat: %w", err)
		}
	}

	return &bridgev2.ResolveIdentifierResponse{
		UserID:   userID,
		UserInfo: responderUserInfoOrDefault(responder, agentName, agentContactIdentifiers(agent.ID), true),
		Ghost:    ghost,
		Chat:     chatResp,
	}, nil
}

// resolveModelIdentifier resolves an explicit model alias/ID to a ghost.
func (oc *AIClient) resolveModelIdentifier(ctx context.Context, modelID string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	// Get or create ghost
	userID := modelUserID(modelID)
	var err error
	var ghost *bridgev2.Ghost
	if oc != nil && oc.UserLogin != nil && oc.UserLogin.Bridge != nil {
		ghost, err = oc.UserLogin.Bridge.GetGhostByID(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("failed to get ghost: %w", err)
		}
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

	responder, err := oc.ResolveResponderForModel(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve model responder: %w", err)
	}
	return &bridgev2.ResolveIdentifierResponse{
		UserID:   userID,
		UserInfo: responderUserInfo(responder, modelContactIdentifiers(modelID), false),
		Ghost:    ghost,
		Chat:     chatResp,
	}, nil
}

func (oc *AIClient) modelJoinMember(ctx context.Context, loginID networkid.UserLoginID, modelID, modelName string, info *ModelInfo) bridgev2.ChatMember {
	responder, err := oc.ResolveResponderForModel(ctx, modelID)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Str("model", modelID).Msg("Failed to resolve responder for model join member")
	}
	memberExtra := responderMetadataMap(responder)
	if memberExtra == nil {
		memberExtra = map[string]any{}
	}
	memberExtra["displayname"] = modelName
	userInfo := responderUserInfoOrDefault(responder, modelContactName(modelID, info), modelContactIdentifiers(modelID), false)
	return bridgev2.ChatMember{
		EventSender: bridgev2.EventSender{
			Sender:      modelUserID(modelID),
			SenderLogin: loginID,
		},
		Membership:       event.MembershipJoin,
		UserInfo:         userInfo,
		MemberEventExtra: memberExtra,
	}
}

func (oc *AIClient) createAgentChatWithModel(ctx context.Context, agent *agents.AgentDefinition, modelID string, applyModelOverride bool) (*bridgev2.CreateChatResponse, error) {
	if !oc.agentsEnabledForLogin() {
		return nil, agentChatsDisabledError()
	}
	if modelID == "" {
		modelID = oc.agentDefaultModel(agent)
	}

	agentName := oc.resolveAgentDisplayName(ctx, agent)
	portal, chatInfo, err := oc.initPortalForChat(ctx, PortalInitOpts{
		ModelID: modelID,
		Title:   fmt.Sprintf("Chat with %s", agentName),
	})
	if err != nil {
		return nil, err
	}

	// Set agent-specific metadata
	pm := portalMeta(portal)

	agentGhostID := oc.agentUserID(agent.ID)

	// Update the OtherUserID to be the agent ghost
	portal.OtherUserID = agentGhostID
	pm.ResolvedTarget = resolveTargetFromGhostID(agentGhostID)
	if applyModelOverride {
		pm.RuntimeModelOverride = ResolveAlias(modelID)
	}
	agentAvatar := strings.TrimSpace(agent.AvatarURL)
	if agentAvatar == "" {
		agentAvatar = strings.TrimSpace(agents.DefaultAgentAvatarMXC)
	}
	if agentAvatar != "" {
		portal.AvatarID = networkid.AvatarID(agentAvatar)
		portal.AvatarMXC = id.ContentURIString(agentAvatar)
	}

	if err := portal.Save(ctx); err != nil {
		return nil, fmt.Errorf("failed to save portal with agent config: %w", err)
	}
	oc.ensureAgentGhostDisplayName(ctx, agent.ID, modelID, agentName)

	// Update chat info members to use agent ghost only
	oc.applyAgentChatInfo(ctx, chatInfo, agent.ID, agentName, modelID)

	// Rooms created via provisioning (ResolveIdentifier/CreateDM) won't go through our explicit
	// post-CreateMatrixRoom call sites. Schedule the welcome notice + auto-greeting for when the
	// Matrix room ID becomes available.
	oc.scheduleWelcomeMessage(ctx, portal.PortalKey)

	return &bridgev2.CreateChatResponse{
		PortalKey: portal.PortalKey,
		// Return the full ChatInfo so bridgev2 can apply ExtraUpdates (initial room state,
		// welcome notice, etc.) when creating the Matrix room via provisioning (CreateDM).
		PortalInfo: chatInfo,
	}, nil
}

// createNewChat creates a new portal for a specific model
func (oc *AIClient) createNewChat(ctx context.Context, modelID string) (*bridgev2.CreateChatResponse, error) {
	portal, chatInfo, err := oc.initPortalForChat(ctx, PortalInitOpts{
		ModelID: modelID,
	})
	if err != nil {
		return nil, err
	}

	// Rooms created via provisioning (ResolveIdentifier/CreateDM) won't go through our explicit
	// post-CreateMatrixRoom call sites. Schedule the welcome notice for when the Matrix room exists.
	oc.scheduleWelcomeMessage(ctx, portal.PortalKey)

	return &bridgev2.CreateChatResponse{
		PortalKey:  portal.PortalKey,
		PortalInfo: chatInfo,
		Portal:     portal,
	}, nil
}

// allocateNextChatIndex increments and returns the next chat index for this login
func (oc *AIClient) allocateNextChatIndex(ctx context.Context) (int, error) {
	oc.chatLock.Lock()
	defer oc.chatLock.Unlock()

	var next int
	if err := oc.updateLoginState(ctx, func(state *loginRuntimeState) bool {
		state.NextChatIndex++
		next = state.NextChatIndex
		return true
	}); err != nil {
		return 0, fmt.Errorf("failed to save login state: %w", err)
	}
	return next, nil
}

// PortalInitOpts contains options for initializing a chat portal
type PortalInitOpts struct {
	ModelID   string
	Title     string
	CopyFrom  *PortalMetadata // For forked chats - copies config from source
	PortalKey *networkid.PortalKey
}

func cloneForkPortalMetadata(src *PortalMetadata, slug, title string) *PortalMetadata {
	if src == nil {
		return nil
	}
	clone := &PortalMetadata{
		Slug:  slug,
		Title: title,
	}
	if src.ResolvedTarget != nil {
		target := *src.ResolvedTarget
		clone.ResolvedTarget = &target
	}
	return clone
}

// initPortalForChat handles common portal initialization logic.
// Returns the configured portal, chat info, and any error.
func (oc *AIClient) initPortalForChat(ctx context.Context, opts PortalInitOpts) (*bridgev2.Portal, *bridgev2.ChatInfo, error) {
	chatIndex, err := oc.allocateNextChatIndex(ctx)
	if err != nil {
		return nil, nil, err
	}

	slug := formatChatSlug(chatIndex)
	modelID := opts.ModelID
	if modelID == "" {
		modelID = oc.effectiveModel(nil)
	}

	title := opts.Title
	if title == "" {
		modelName := modelContactName(modelID, oc.findModelInfo(modelID))
		title = fmt.Sprintf("AI Chat with %s", modelName)
	}

	portalKey := portalKeyForChat(oc.UserLogin.ID)
	if opts.PortalKey != nil {
		portalKey = *opts.PortalKey
	}
	portal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get portal: %w", err)
	}

	// Initialize or copy metadata
	var pmeta *PortalMetadata
	if opts.CopyFrom != nil {
		pmeta = cloneForkPortalMetadata(opts.CopyFrom, slug, title)
	} else {
		pmeta = &PortalMetadata{
			Slug:  slug,
			Title: title,
		}
	}
	portal.Metadata = pmeta

	if err := sdk.ConfigureDMPortal(ctx, sdk.ConfigureDMPortalParams{
		Portal:      portal,
		Title:       title,
		OtherUserID: modelUserID(modelID),
		Save:        true,
		MutatePortal: func(portal *bridgev2.Portal) {
			defaultAvatar := strings.TrimSpace(agents.DefaultAgentAvatarMXC)
			if defaultAvatar != "" {
				portal.AvatarID = networkid.AvatarID(defaultAvatar)
				portal.AvatarMXC = id.ContentURIString(defaultAvatar)
			}
		},
	}); err != nil {
		return nil, nil, fmt.Errorf("failed to save portal: %w", err)
	}
	oc.ensureGhostDisplayName(ctx, modelID)

	chatInfo := oc.composeChatInfo(ctx, title, modelID)
	return portal, chatInfo, nil
}

// handleNewChat creates a new chat using the current room's agent/model,
// or an explicitly provided agent/model.
func (oc *AIClient) handleNewChat(
	ctx context.Context,
	_ *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	args []string,
) {
	runCtx := oc.backgroundContext(ctx)
	agent, modelID, err := oc.resolveNewChatTarget(runCtx, meta, args)
	if err != nil {
		oc.sendSystemNotice(runCtx, portal, err.Error())
		return
	}
	if agent != nil {
		oc.createAndOpenAgentChat(runCtx, portal, agent, modelID, false)
		return
	}
	oc.createAndOpenModelChat(runCtx, portal, modelID)
}

func (oc *AIClient) validateNewChatCommand(
	ctx context.Context,
	_ *bridgev2.Portal,
	meta *PortalMetadata,
	args []string,
) error {
	_, _, err := oc.resolveNewChatTarget(ctx, meta, args)
	return err
}

func (oc *AIClient) resolveNewChatTarget(
	ctx context.Context,
	meta *PortalMetadata,
	args []string,
) (*agents.AgentDefinition, string, error) {
	const usage = "usage: !ai new [agent <agent_id>]"

	if len(args) >= 2 {
		cmd := strings.ToLower(args[0])
		if cmd != "agent" {
			return nil, "", errors.New(usage)
		}
		if !oc.agentsEnabledForLogin() {
			return nil, "", agentChatsDisabledError()
		}
		targetID := args[1]
		if targetID == "" || len(args) > 2 {
			return nil, "", errors.New(usage)
		}
		store := NewAgentStoreAdapter(oc)
		agent, err := store.GetAgentByID(ctx, targetID)
		if err != nil || agent == nil {
			return nil, "", fmt.Errorf("agent not found: %s", targetID)
		}
		modelID, err := oc.resolveAgentModelForNewChat(ctx, agent, "")
		if err != nil {
			return nil, "", err
		}
		return agent, modelID, nil
	} else if len(args) == 1 {
		return nil, "", errors.New(usage)
	}

	if meta == nil {
		return nil, "", fmt.Errorf("couldn't resolve the current chat target")
	}
	agentID := resolveAgentID(meta)
	if agentID != "" {
		if !oc.agentsEnabledForLogin() {
			return nil, "", agentChatsDisabledError()
		}
		store := NewAgentStoreAdapter(oc)
		agent, err := store.GetAgentByID(ctx, agentID)
		if err != nil || agent == nil {
			return nil, "", fmt.Errorf("agent not found: %s", agentID)
		}
		modelID, err := oc.resolveAgentModelForNewChat(ctx, agent, oc.effectiveModel(meta))
		if err != nil {
			return nil, "", err
		}
		return agent, modelID, nil
	}

	modelID := oc.effectiveModel(meta)
	if modelID == "" {
		return nil, "", fmt.Errorf("no model configured for this room")
	}
	if ok, _ := oc.validateModel(ctx, modelID); !ok {
		return nil, "", fmt.Errorf("that model isn't available: %s", modelID)
	}
	return nil, modelID, nil
}

func (oc *AIClient) resolveAgentModelForNewChat(ctx context.Context, agent *agents.AgentDefinition, preferredModel string) (string, error) {
	if preferredModel != "" {
		if ok, _ := oc.validateModel(ctx, preferredModel); ok {
			return preferredModel, nil
		}
	}

	if agent != nil {
		defaultModel := oc.agentDefaultModel(agent)
		if ok, _ := oc.validateModel(ctx, defaultModel); ok {
			return defaultModel, nil
		}
	}

	fallback := oc.effectiveModel(nil)
	if fallback != "" {
		if ok, _ := oc.validateModel(ctx, fallback); ok {
			return fallback, nil
		}
	}

	if preferredModel != "" {
		return "", fmt.Errorf("that model isn't available: %s", preferredModel)
	}
	return "", errors.New("no available model")
}

func (oc *AIClient) createAndOpenAgentChat(ctx context.Context, portal *bridgev2.Portal, agent *agents.AgentDefinition, modelID string, modelOverride bool) {
	agentName := oc.resolveAgentDisplayName(ctx, agent)
	chatResp, err := oc.createAgentChatWithModel(ctx, agent, modelID, modelOverride)
	if err != nil {
		oc.sendSystemNotice(ctx, portal, "Couldn't create the chat: "+err.Error())
		return
	}

	newPortal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, chatResp.PortalKey)
	if err != nil || newPortal == nil {
		msg := "Couldn't open the new chat."
		if err != nil {
			msg = "Couldn't open the new chat: " + err.Error()
		}
		oc.sendSystemNotice(ctx, portal, msg)
		return
	}

	chatInfo := chatResp.PortalInfo
	if err := oc.materializePortalRoom(ctx, newPortal, chatInfo, portalRoomMaterializeOptions{SendWelcome: true}); err != nil {
		oc.sendSystemNotice(ctx, portal, "Couldn't create the room: "+err.Error())
		return
	}

	roomLink := fmt.Sprintf("https://matrix.to/#/%s", newPortal.MXID)
	oc.sendSystemNotice(ctx, portal, fmt.Sprintf(
		"New %s chat created.\nOpen: %s",
		agentName, roomLink,
	))
}

func (oc *AIClient) createAndOpenModelChat(ctx context.Context, portal *bridgev2.Portal, modelID string) {
	chatResp, err := oc.createNewChat(ctx, modelID)
	if err != nil {
		oc.sendSystemNotice(ctx, portal, "Couldn't create the chat: "+err.Error())
		return
	}

	newPortal := chatResp.Portal
	chatInfo := chatResp.PortalInfo
	if err := oc.materializePortalRoom(ctx, newPortal, chatInfo, portalRoomMaterializeOptions{SendWelcome: true}); err != nil {
		oc.sendSystemNotice(ctx, portal, "Couldn't create the room: "+err.Error())
		return
	}

	roomLink := fmt.Sprintf("https://matrix.to/#/%s", newPortal.MXID)
	oc.sendSystemNotice(ctx, portal, fmt.Sprintf(
		"New %s chat created.\nOpen: %s",
		modelContactName(modelID, oc.findModelInfo(modelID)), roomLink,
	))
}

// chatInfoFromPortal builds ChatInfo from an existing portal
func (oc *AIClient) chatInfoFromPortal(ctx context.Context, portal *bridgev2.Portal) *bridgev2.ChatInfo {
	meta := portalMeta(portal)
	modelID := oc.effectiveModel(meta)
	title := meta.Title
	if title == "" {
		if portal.Name != "" {
			title = portal.Name
		} else {
			title = modelContactName(modelID, oc.findModelInfo(modelID))
		}
	}
	chatInfo := oc.composeChatInfo(ctx, title, modelID)

	agentID := resolveAgentID(meta)
	if agentID == "" || !oc.agentsEnabledForLogin() {
		return chatInfo
	}

	agentName := agentID
	// Try preset first - guaranteed to work for built-in agents (like "beeper")
	if preset := agents.GetPresetByID(agentID); preset != nil {
		agentName = oc.resolveAgentDisplayName(ctx, preset)
	} else if ctx != nil {
		// Custom agent - need Matrix state lookup
		store := NewAgentStoreAdapter(oc)
		if agent, err := store.GetAgentByID(ctx, agentID); err == nil && agent != nil {
			agentName = oc.resolveAgentDisplayName(ctx, agent)
		}
	}

	oc.applyAgentChatInfo(ctx, chatInfo, agentID, agentName, modelID)
	return chatInfo
}

// composeChatInfo creates a ChatInfo struct for a chat
func (oc *AIClient) composeChatInfo(ctx context.Context, title, modelID string) *bridgev2.ChatInfo {
	if modelID == "" {
		modelID = oc.effectiveModel(nil)
	}
	modelInfo := oc.findModelInfo(modelID)
	modelName := modelContactName(modelID, modelInfo)
	if title == "" {
		title = modelName
	}
	chatInfo := sdk.BuildLoginDMChatInfo(sdk.LoginDMChatInfoParams{
		Title:             title,
		Login:             oc.UserLogin,
		HumanUserIDPrefix: oc.HumanUserIDPrefix,
		BotUserID:         modelUserID(modelID),
		BotDisplayName:    modelName,
	})
	// Override bot member with model-specific UserInfo and extra fields.
	chatInfo.Members.MemberMap[modelUserID(modelID)] = oc.modelJoinMember(ctx, oc.UserLogin.ID, modelID, modelName, modelInfo)
	return chatInfo
}

func (oc *AIClient) applyAgentChatInfo(ctx context.Context, chatInfo *bridgev2.ChatInfo, agentID, agentName, modelID string) {
	if chatInfo == nil || agentID == "" || !oc.agentsEnabledForLogin() {
		return
	}
	if modelID == "" {
		modelID = oc.effectiveModel(nil)
	}

	agentGhostID := oc.agentUserID(agentID)
	agentDisplayName := agentName

	members := chatInfo.Members
	if members == nil {
		members = &bridgev2.ChatMemberList{}
	}
	if members.MemberMap == nil {
		members.MemberMap = make(bridgev2.ChatMemberMap)
	}
	members.OtherUserID = agentGhostID

	humanID := humanUserID(oc.UserLogin.ID)
	humanMember := members.MemberMap[humanID]
	humanMember.EventSender = bridgev2.EventSender{
		Sender:      humanID,
		IsFromMe:    true,
		SenderLogin: oc.UserLogin.ID,
	}

	agentMember := members.MemberMap[agentGhostID]
	agentMember.EventSender = bridgev2.EventSender{
		Sender:      agentGhostID,
		SenderLogin: oc.UserLogin.ID,
	}
	responder, err := oc.ResolveResponderForAgent(ctx, agentID, ResponderResolveOptions{
		RuntimeModelOverride: modelID,
	})
	if err != nil {
		oc.log.Warn().Err(err).Str("agent", agentID).Str("model", modelID).Msg("Failed to resolve responder for agent chat info")
	}
	agentMember.UserInfo = responderUserInfoOrDefault(responder, agentDisplayName, agentContactIdentifiers(agentID), true)
	agentMember.MemberEventExtra = responderMetadataMap(responder)
	if agentMember.MemberEventExtra == nil {
		agentMember.MemberEventExtra = map[string]any{}
	}
	agentMember.MemberEventExtra["displayname"] = agentDisplayName

	members.MemberMap = bridgev2.ChatMemberMap{
		humanID:      humanMember,
		agentGhostID: agentMember,
	}
	chatInfo.Members = members
}

// BroadcastRoomState refreshes standard Matrix room capabilities and command descriptions.
func (oc *AIClient) BroadcastRoomState(ctx context.Context, portal *bridgev2.Portal) error {
	portal.UpdateCapabilities(ctx, oc.UserLogin, true)
	oc.BroadcastCommandDescriptions(ctx, portal)
	return nil
}

// sendSystemNotice sends an informational notice to the room via the bridge bot.
func (oc *AIClient) sendSystemNotice(ctx context.Context, portal *bridgev2.Portal, message string) {
	if oc == nil {
		return
	}
	if err := sdk.SendSystemMessage(ctx, oc.UserLogin, portal, bridgev2.EventSender{}, message); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to send system notice")
	}
}

// Bootstrap and initialization functions

func (oc *AIClient) scheduleBootstrap() {
	backgroundCtx := oc.UserLogin.Bridge.BackgroundCtx
	oc.bootstrapOnce.Do(func() {
		go oc.bootstrap(backgroundCtx)
	})
}

func (oc *AIClient) bootstrap(ctx context.Context) {
	logCtx := oc.loggerForContext(ctx).With().Str("component", "openai-chat-bootstrap").Logger().WithContext(ctx)
	oc.waitForLoginPersisted(logCtx)

	oc.loggerForContext(ctx).Info().Msg("Starting bootstrap for new login")

	if err := oc.syncChatCounter(logCtx); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to sync chat counter, continuing with default chat creation")
		// Don't return - still create the default chat (matches other bridge patterns)
	}

	if shouldEnsureDefaultChat(loginMetadata(oc.UserLogin)) {
		// Create default chat room with Beep agent
		if err := oc.ensureDefaultChat(logCtx); err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to ensure default chat")
			return
		}
	}
	oc.loggerForContext(ctx).Info().Msg("Bootstrap completed successfully")
}

func (oc *AIClient) waitForLoginPersisted(ctx context.Context) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(60 * time.Second)
	for {
		_, err := oc.UserLogin.Bridge.DB.UserLogin.GetByID(ctx, oc.UserLogin.ID)
		if err == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-timeout:
			oc.loggerForContext(ctx).Warn().Msg("Timed out waiting for login to persist, continuing anyway")
			return
		case <-ticker.C:
		}
	}
}

func (oc *AIClient) syncChatCounter(ctx context.Context) error {
	portals, err := oc.listAllChatPortals(ctx)
	if err != nil {
		return err
	}
	state := oc.loginStateSnapshot(ctx)
	maxIdx := state.NextChatIndex
	for _, portal := range portals {
		pm := portalMeta(portal)
		if idx, ok := parseChatSlug(pm.Slug); ok && idx > maxIdx {
			maxIdx = idx
		}
	}
	if maxIdx > state.NextChatIndex {
		return oc.updateLoginState(ctx, func(state *loginRuntimeState) bool {
			if maxIdx <= state.NextChatIndex {
				return false
			}
			state.NextChatIndex = maxIdx
			return true
		})
	}
	return nil
}

func (oc *AIClient) ensureDefaultChat(ctx context.Context) error {
	oc.loggerForContext(ctx).Debug().Msg("Ensuring default AI chat room exists")
	defaultPortalKey := defaultChatPortalKey(oc.UserLogin.ID)
	deterministicPortalBlocked := false

	portal, err := oc.UserLogin.Bridge.GetExistingPortalByKey(ctx, defaultPortalKey)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to load default chat portal by deterministic key")
	} else if portal != nil && isDefaultChatCandidate(portal) {
		return oc.ensureExistingChatPortalReady(ctx, portal, "Existing default chat already has MXID", "Default chat missing MXID; creating Matrix room", "Failed to create Matrix room for default chat")
	} else if portal != nil {
		deterministicPortalBlocked = true
		oc.loggerForContext(ctx).Warn().Stringer("portal", portal.PortalKey).Msg("Ignoring hidden deterministic default chat portal")
	}

	portals, err := oc.listAllChatPortals(ctx)
	if err != nil {
		oc.loggerForContext(ctx).Err(err).Msg("Failed to list chat portals")
		return err
	}

	defaultPortal := chooseDefaultChatPortal(portals)

	if defaultPortal != nil {
		return oc.ensureExistingChatPortalReady(ctx, defaultPortal, "Existing chat already has MXID", "Existing portal missing MXID; creating Matrix room", "Failed to create Matrix room for existing portal")
	}

	// Create default chat with Beep agent
	beeperAgent := agents.GetBeeperAI()
	if beeperAgent == nil {
		return errors.New("beeper AI agent not found")
	}

	// Determine model from agent config or use default
	modelID := beeperAgent.Model.Primary
	if modelID == "" {
		modelID = oc.effectiveModel(nil)
	}

	initOpts := PortalInitOpts{
		ModelID: modelID,
		Title:   "New AI Chat",
	}
	if !deterministicPortalBlocked {
		initOpts.PortalKey = &defaultPortalKey
	}
	portal, chatInfo, err := oc.initPortalForChat(ctx, initOpts)
	if err != nil {
		existingPortal, existingErr := oc.UserLogin.Bridge.GetExistingPortalByKey(ctx, defaultPortalKey)
		if !deterministicPortalBlocked && existingErr == nil && existingPortal != nil {
			if existingPortal.MXID != "" {
				oc.loggerForContext(ctx).Debug().Stringer("portal", existingPortal.PortalKey).Msg("Existing default chat already has MXID")
				return nil
			}
			info := oc.chatInfoFromPortal(ctx, existingPortal)
			oc.loggerForContext(ctx).Info().Stringer("portal", existingPortal.PortalKey).Msg("Default chat missing MXID; creating Matrix room")
			createErr := oc.materializePortalRoom(ctx, existingPortal, info, portalRoomMaterializeOptions{SendWelcome: true})
			if createErr != nil {
				oc.loggerForContext(ctx).Err(createErr).Msg("Failed to create Matrix room for default chat")
				return createErr
			}
			oc.loggerForContext(ctx).Info().Stringer("portal", existingPortal.PortalKey).Msg("New AI Chat room created")
			return nil
		}
		oc.loggerForContext(ctx).Err(err).Msg("Failed to create default portal")
		return err
	}

	// Set agent-specific metadata
	pm := portalMeta(portal)

	// Update the OtherUserID to be the agent ghost
	agentGhostID := oc.agentUserID(beeperAgent.ID)
	portal.OtherUserID = agentGhostID
	pm.ResolvedTarget = resolveTargetFromGhostID(agentGhostID)

	if err := portal.Save(ctx); err != nil {
		oc.loggerForContext(ctx).Err(err).Msg("Failed to save portal with agent config")
		return err
	}

	// Update chat info members to use agent ghost only
	agentName := oc.resolveAgentDisplayName(ctx, beeperAgent)
	oc.applyAgentChatInfo(ctx, chatInfo, beeperAgent.ID, agentName, modelID)
	oc.ensureAgentGhostDisplayName(ctx, beeperAgent.ID, modelID, agentName)

	err = oc.materializePortalRoom(ctx, portal, chatInfo, portalRoomMaterializeOptions{SendWelcome: true})
	if err != nil {
		oc.loggerForContext(ctx).Err(err).Msg("Failed to create Matrix room for default chat")
		return err
	}
	oc.loggerForContext(ctx).Info().Stringer("portal", portal.PortalKey).Msg("New AI Chat room created")
	return nil
}

func (oc *AIClient) ensureExistingChatPortalReady(ctx context.Context, portal *bridgev2.Portal, readyMsg string, createMsg string, errMsg string) error {
	if !isDefaultChatCandidate(portal) {
		return fmt.Errorf("portal %s is hidden and can't be selected as default chat", portal.PortalKey)
	}
	if portal.MXID != "" {
		oc.loggerForContext(ctx).Debug().Stringer("portal", portal.PortalKey).Msg(readyMsg)
		return nil
	}
	info := oc.chatInfoFromPortal(ctx, portal)
	oc.loggerForContext(ctx).Info().Stringer("portal", portal.PortalKey).Msg(createMsg)
	err := oc.materializePortalRoom(ctx, portal, info, portalRoomMaterializeOptions{SendWelcome: true})
	if err != nil {
		oc.loggerForContext(ctx).Err(err).Msg(errMsg)
		return err
	}
	return nil
}

func isDefaultChatCandidate(portal *bridgev2.Portal) bool {
	return portal != nil && !shouldExcludeModelVisiblePortal(portalMeta(portal))
}

func chooseDefaultChatPortal(portals []*bridgev2.Portal) *bridgev2.Portal {
	var defaultPortal *bridgev2.Portal
	var (
		minIdx   int
		haveSlug bool
	)
	for _, portal := range portals {
		if !isDefaultChatCandidate(portal) {
			continue
		}
		pm := portalMeta(portal)
		if idx, ok := parseChatSlug(pm.Slug); ok {
			if !haveSlug || idx < minIdx {
				minIdx = idx
				defaultPortal = portal
				haveSlug = true
			}
		} else if defaultPortal == nil && !haveSlug {
			defaultPortal = portal
		}
	}
	return defaultPortal
}

func (oc *AIClient) listAllChatPortals(ctx context.Context) ([]*bridgev2.Portal, error) {
	// Query all portals and filter by receiver (our login ID)
	// This works because all our portals have Receiver set to our UserLogin.ID
	allDBPortals, err := oc.UserLogin.Bridge.DB.Portal.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	portals := make([]*bridgev2.Portal, 0)
	for _, dbPortal := range allDBPortals {
		// Filter to only portals owned by this user login
		if dbPortal.Receiver != oc.UserLogin.ID {
			continue
		}
		portal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, dbPortal.PortalKey)
		if err != nil {
			return nil, err
		}
		if portal != nil {
			portals = append(portals, portal)
		}
	}
	return portals, nil
}

// HandleMatrixMessageRemove handles message deletions from Matrix
// For AI Chats, delete only local state; there is no remote service to sync.
func (oc *AIClient) HandleMatrixMessageRemove(ctx context.Context, msg *bridgev2.MatrixMessageRemove) error {
	oc.loggerForContext(ctx).Debug().
		Stringer("event_id", msg.TargetMessage.MXID).
		Stringer("portal", msg.Portal.PortalKey).
		Msg("Handling message deletion")

	// Delete from our database - the Matrix side is already handled by the bridge framework
	if err := oc.UserLogin.Bridge.DB.Message.Delete(ctx, msg.TargetMessage.RowID); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Stringer("event_id", msg.TargetMessage.MXID).Msg("Failed to delete message from database")
		return err
	}
	oc.notifySessionMutation(ctx, msg.Portal, portalMeta(msg.Portal), true)

	return nil
}

// HandleMatrixDisappearingTimer handles disappearing message timer changes from Matrix
// For AI Chats, update only the portal disappear field; the bridge framework handles deletion.
func (oc *AIClient) HandleMatrixDisappearingTimer(ctx context.Context, msg *bridgev2.MatrixDisappearingTimer) (bool, error) {
	oc.loggerForContext(ctx).Debug().
		Stringer("portal", msg.Portal.PortalKey).
		Str("type", string(msg.Content.Type)).
		Dur("timer", msg.Content.Timer.Duration).
		Msg("Handling disappearing timer change")

	// Convert event to database setting and update portal
	setting := database.DisappearingSettingFromEvent(msg.Content)
	changed := msg.Portal.UpdateDisappearingSetting(ctx, setting, bridgev2.UpdateDisappearingSettingOpts{
		Save: true,
	})

	return changed, nil
}
