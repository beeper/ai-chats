package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/beeper/agentremote/pkg/agents"
	"github.com/beeper/agentremote/pkg/agents/tools"
	"github.com/beeper/agentremote/pkg/shared/bridgeutil"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/pkg/shared/toolspec"
	"github.com/beeper/agentremote/sdk"

	"go.mau.fi/util/ptr"
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
	cfg := oc.loginConfigSnapshot(context.Background())
	return agentsEnabledForLoginConfig(cfg)
}

func agentsEnabledForLoginConfig(cfg *aiLoginConfig) bool {
	return cfg != nil && cfg.Agents != nil && *cfg.Agents
}

func shouldEnsureDefaultChat(cfg *aiLoginConfig) bool {
	if cfg == nil {
		return false
	}
	return agentsEnabledForLoginConfig(cfg)
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
	provider := loginMetadata(oc.UserLogin).Provider
	loginCfg := oc.loginConfigSnapshot(context.Background())
	if strings.TrimSpace(oc.connector.resolveProviderAPIKeyForConfig(provider, loginCfg)) == "" {
		return false
	}
	switch provider {
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

func (oc *AIClient) hydrateContactResponseGhost(ctx context.Context, resp *bridgev2.ResolveIdentifierResponse, field, value string) *bridgev2.ResolveIdentifierResponse {
	if resp == nil || resp.UserID == "" || oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return resp
	}
	ghost, err := oc.resolveChatGhost(ctx, resp.UserID)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Str(field, value).Msg("Failed to hydrate ghost for contact")
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
	results, err := oc.collectContactResponses(ctx, query)
	if err != nil {
		return nil, err
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
	contacts, err := oc.collectContactResponses(ctx, "")
	if err != nil {
		oc.loggerForContext(ctx).Error().Err(err).Msg("Failed to load contacts")
		return nil, err
	}

	oc.loggerForContext(ctx).Info().Int("count", len(contacts)).Msg("Returning contact list")
	return contacts, nil
}

func (oc *AIClient) collectContactResponses(ctx context.Context, query string) ([]*bridgev2.ResolveIdentifierResponse, error) {
	query = strings.ToLower(strings.TrimSpace(query))

	agentsList, err := oc.sdkAgentCatalog().ListAgents(ctx, oc.UserLogin)
	if err != nil {
		return nil, fmt.Errorf("failed to load agents: %w", err)
	}

	results := make([]*bridgev2.ResolveIdentifierResponse, 0, len(agentsList))
	seen := make(map[networkid.UserID]struct{})
	appendResponse := func(resp *bridgev2.ResolveIdentifierResponse) {
		if resp == nil {
			return
		}
		if _, ok := seen[resp.UserID]; ok {
			return
		}
		results = append(results, resp)
		seen[resp.UserID] = struct{}{}
	}

	for _, agent := range agentsList {
		if query != "" && !agentMatchesQuery(query, agent) {
			continue
		}
		if agent == nil || !oc.agentsEnabledForLogin() {
			continue
		}
		resp := &bridgev2.ResolveIdentifierResponse{
			UserID: networkid.UserID(agent.ID),
		}
		if agentInfo := agent.UserInfo(); agentInfo != nil {
			resp.UserInfo = agentInfo
		}
		if agentID := catalogAgentID(agent); agentID != "" {
			responder, err := oc.resolveResponder(ctx, &PortalMetadata{
				ResolvedTarget: &ResolvedTarget{
					Kind:    ResolvedTargetAgent,
					AgentID: agentID,
				},
			}, ResponderResolveOptions{})
			if err != nil {
				oc.loggerForContext(ctx).Warn().Err(err).Str("agent", agentID).Msg("Failed to resolve responder for agent contact")
			} else if resp.UserInfo == nil {
				resp.UserInfo = responderUserInfo(responder, agent.Identifiers, true)
			} else {
				resp.UserInfo.ExtraProfile = responderExtraProfile(responder)
			}
		}
		if resp.UserInfo != nil {
			resp = oc.hydrateContactResponseGhost(ctx, resp, "agent", string(resp.UserID))
		}
		appendResponse(resp)
	}

	models, err := oc.listAvailableModels(ctx, false)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to load model contacts")
		return results, nil
	}
	for i := range models {
		model := &models[i]
		if model.ID == "" {
			continue
		}
		if query != "" && !modelMatchesQuery(query, model) {
			continue
		}
		responder, err := oc.resolveResponder(ctx, &PortalMetadata{
			ResolvedTarget: &ResolvedTarget{
				Kind:    ResolvedTargetModel,
				ModelID: model.ID,
			},
		}, ResponderResolveOptions{})
		if err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Str("model", model.ID).Msg("Failed to resolve responder for model contact")
		}
		appendResponse(oc.hydrateContactResponseGhost(ctx, &bridgev2.ResolveIdentifierResponse{
			UserID:   modelUserID(model.ID),
			UserInfo: responderUserInfoOrDefault(responder, modelContactName(model.ID, model), modelContactIdentifiers(model.ID), false),
		}, "model", model.ID))
	}
	return results, nil
}

type chatResolveTarget struct {
	agent         *agents.AgentDefinition
	modelID       string
	modelRedirect networkid.UserID
	response      *bridgev2.ResolveIdentifierResponse
}

func parseChatGhostTarget(ghostID string) (modelID string, agentID string) {
	if modelID = parseModelFromGhostID(ghostID); modelID != "" {
		return modelID, ""
	}
	if agentID, ok := parseAgentFromGhostID(ghostID); ok {
		return "", agentID
	}
	return "", ""
}

func normalizeChatIdentifier(identifier string) string {
	id := strings.TrimSpace(identifier)
	if canonicalModelID := parseCanonicalModelIdentifier(id); canonicalModelID != "" {
		return canonicalModelID
	}
	if canonicalAgentID := parseCanonicalAgentIdentifier(id); canonicalAgentID != "" {
		return canonicalAgentID
	}
	return id
}

func (oc *AIClient) resolveModelChatTarget(ctx context.Context, identifier string) (*chatResolveTarget, error) {
	resolved, valid, err := oc.resolveModelID(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if !valid || resolved == "" {
		return nil, nil
	}
	return &chatResolveTarget{
		modelID:       resolved,
		modelRedirect: modelRedirectTarget(identifier, resolved),
	}, nil
}

func (oc *AIClient) resolveAgentChatTarget(ctx context.Context, agentID string) (*chatResolveTarget, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, nil
	}
	agent, err := (&AgentStoreAdapter{client: oc}).GetAgentByID(ctx, agentID)
	if err != nil || agent == nil {
		return nil, bridgev2.WrapRespErr(fmt.Errorf("agent '%s' not found", agentID), mautrix.MNotFound)
	}
	return &chatResolveTarget{agent: agent}, nil
}

func (oc *AIClient) resolveParsedChatGhostTarget(ctx context.Context, modelID string, agentID string) (*chatResolveTarget, bool, error) {
	if modelID == "" && agentID == "" {
		return nil, false, nil
	}
	if agentID != "" {
		target, err := oc.resolveAgentChatTarget(ctx, agentID)
		return target, true, err
	}
	target, err := oc.resolveModelChatTarget(ctx, modelID)
	if err != nil {
		return nil, true, err
	}
	if target == nil {
		return nil, true, bridgev2.WrapRespErr(fmt.Errorf("model '%s' not found", modelID), mautrix.MNotFound)
	}
	return target, true, nil
}

func (oc *AIClient) resolveChatTargetFromIdentifier(ctx context.Context, identifier string) (*chatResolveTarget, error) {
	id := normalizeChatIdentifier(identifier)
	if id == "" {
		return nil, bridgev2.WrapRespErr(errors.New("identifier is required"), mautrix.MInvalidParam)
	}
	modelID, agentID := parseChatGhostTarget(id)
	if target, resolved, err := oc.resolveParsedChatGhostTarget(ctx, modelID, agentID); resolved {
		if err != nil {
			return nil, err
		}
		return target, nil
	}
	if catalogAgent, err := oc.sdkAgentCatalog().ResolveAgent(ctx, oc.UserLogin, id); err == nil && catalogAgent != nil {
		agentID := catalogAgentID(catalogAgent)
		if agentID == "" {
			if oc.agentsEnabledForLogin() {
				resp := &bridgev2.ResolveIdentifierResponse{
					UserID: networkid.UserID(catalogAgent.ID),
				}
				if agentInfo := catalogAgent.UserInfo(); agentInfo != nil {
					resp.UserInfo = agentInfo
				}
				if resp.UserInfo != nil {
					resp = oc.hydrateContactResponseGhost(ctx, resp, "agent", string(resp.UserID))
				}
				return &chatResolveTarget{response: resp}, nil
			}
			return nil, bridgev2.WrapRespErr(fmt.Errorf("agent '%s' not found", id), mautrix.MNotFound)
		}
		return oc.resolveAgentChatTarget(ctx, agentID)
	}
	target, err := oc.resolveModelChatTarget(ctx, id)
	if err != nil {
		return nil, err
	}
	if target != nil {
		return target, nil
	}
	return nil, bridgev2.WrapRespErr(fmt.Errorf("identifier '%s' not found", id), mautrix.MNotFound)
}

func (oc *AIClient) resolveChatTargetFromGhost(ctx context.Context, ghost *bridgev2.Ghost) (*chatResolveTarget, error) {
	if ghost == nil {
		return nil, bridgev2.WrapRespErr(errors.New("ghost is required"), mautrix.MInvalidParam)
	}
	ghostID := string(ghost.ID)
	modelID, agentID := parseChatGhostTarget(ghostID)
	if target, resolved, err := oc.resolveParsedChatGhostTarget(ctx, modelID, agentID); resolved {
		if err != nil {
			return nil, err
		}
		return target, nil
	}
	return nil, bridgev2.WrapRespErr(fmt.Errorf("unsupported ghost ID: %s", ghostID), mautrix.MInvalidParam)
}

func (oc *AIClient) resolveChatTargetResponse(ctx context.Context, target *chatResolveTarget, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	if target == nil {
		return nil, bridgev2.WrapRespErr(errors.New("identifier target is required"), mautrix.MInvalidParam)
	}
	if target.response != nil {
		return target.response, nil
	}
	switch {
	case target.agent != nil:
		if !oc.agentsEnabledForLogin() {
			return nil, agentChatsDisabledError()
		}
		agent := target.agent
		modelID := oc.agentDefaultModel(agent)
		userID := oc.agentUserID(agent.ID)
		ghost, err := oc.resolveChatGhost(ctx, userID)
		if err != nil {
			return nil, err
		}

		agentName := oc.resolveAgentDisplayName(ctx, agent)
		if agentName == "" {
			agentName = strings.TrimSpace(agent.EffectiveName())
		}
		if agentName == "" {
			agentName = agent.ID
		}
		oc.ensureAgentGhostDisplayName(ctx, agent.ID, modelID, agentName)
		responder, err := oc.resolveResponder(ctx, &PortalMetadata{
			ResolvedTarget: &ResolvedTarget{
				Kind:    ResolvedTargetAgent,
				AgentID: agent.ID,
			},
		}, ResponderResolveOptions{
			RuntimeModelOverride: modelID,
		})
		if err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Str("agent", agent.ID).Msg("Failed to resolve responder for agent identifier")
			responder = nil
		}

		var chatResp *bridgev2.CreateChatResponse
		if createChat {
			oc.loggerForContext(ctx).Info().Str("agent", agent.ID).Msg("Creating new chat")
			chatResp, err = oc.createChat(ctx, chatCreateParams{
				ModelID: modelID,
				Agent:   agent,
			})
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
	case target.modelID != "":
		modelID := target.modelID
		userID := modelUserID(modelID)
		ghost, err := oc.resolveChatGhost(ctx, userID)
		if err != nil {
			return nil, err
		}

		oc.ensureGhostDisplayName(ctx, modelID)

		var chatResp *bridgev2.CreateChatResponse
		if createChat {
			oc.loggerForContext(ctx).Info().Str("model", modelID).Msg("Creating new chat")
			chatResp, err = oc.createChat(ctx, chatCreateParams{ModelID: modelID})
			if err != nil {
				return nil, fmt.Errorf("failed to create chat: %w", err)
			}
		}

		responder, err := oc.resolveResponder(ctx, &PortalMetadata{
			ResolvedTarget: &ResolvedTarget{
				Kind:    ResolvedTargetModel,
				ModelID: modelID,
			},
		}, ResponderResolveOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to resolve model responder: %w", err)
		}
		resp := &bridgev2.ResolveIdentifierResponse{
			UserID:   userID,
			UserInfo: responderUserInfo(responder, modelContactIdentifiers(modelID), false),
			Ghost:    ghost,
			Chat:     chatResp,
		}
		if createChat && resp.Chat != nil && target.modelRedirect != "" {
			resp.Chat.DMRedirectedTo = target.modelRedirect
		}
		return resp, nil
	default:
		return nil, bridgev2.WrapRespErr(errors.New("identifier target is required"), mautrix.MInvalidParam)
	}
}

func (oc *AIClient) resolveChatGhost(ctx context.Context, userID networkid.UserID) (*bridgev2.Ghost, error) {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || userID == "" {
		return nil, nil
	}
	ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get ghost: %w", err)
	}
	return ghost, nil
}

// ResolveIdentifier resolves an agent ID to a ghost and optionally creates a chat.
func (oc *AIClient) ResolveIdentifier(ctx context.Context, identifier string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	target, err := oc.resolveChatTargetFromIdentifier(ctx, identifier)
	if err != nil {
		return nil, err
	}
	return oc.resolveChatTargetResponse(ctx, target, createChat)
}

// CreateChatWithGhost creates a DM for a known model or agent ghost.
func (oc *AIClient) CreateChatWithGhost(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.CreateChatResponse, error) {
	target, err := oc.resolveChatTargetFromGhost(ctx, ghost)
	if err != nil {
		return nil, err
	}
	resp, err := oc.resolveChatTargetResponse(ctx, target, true)
	if err != nil || resp == nil {
		return nil, err
	}
	return resp.Chat, nil
}

func (oc *AIClient) modelJoinMember(ctx context.Context, loginID networkid.UserLoginID, modelID, modelName string, info *ModelInfo) bridgev2.ChatMember {
	responder, err := oc.resolveResponder(ctx, &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{
			Kind:    ResolvedTargetModel,
			ModelID: modelID,
		},
	}, ResponderResolveOptions{})
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

type chatCreateParams struct {
	ModelID            string
	Agent              *agents.AgentDefinition
	ApplyModelOverride bool
	Title              string
	PortalKey          *networkid.PortalKey
}

func (oc *AIClient) createChat(ctx context.Context, params chatCreateParams) (*bridgev2.CreateChatResponse, error) {
	modelID := strings.TrimSpace(params.ModelID)
	initOpts := PortalInitOpts{
		ModelID:   modelID,
		Title:     strings.TrimSpace(params.Title),
		PortalKey: params.PortalKey,
	}
	if params.Agent != nil {
		if !oc.agentsEnabledForLogin() {
			return nil, agentChatsDisabledError()
		}
		if modelID == "" {
			modelID = oc.agentDefaultModel(params.Agent)
			initOpts.ModelID = modelID
		}
		if initOpts.Title == "" {
			initOpts.Title = fmt.Sprintf("Chat with %s", oc.resolveAgentDisplayName(ctx, params.Agent))
		}
	}

	portal, chatInfo, err := oc.initPortalForChat(ctx, initOpts)
	if err != nil {
		return nil, err
	}
	if params.Agent != nil {
		oc.configureAgentChatPortal(ctx, portal, chatInfo, params.Agent, modelID, params.ApplyModelOverride, "agent config")
	}

	return &bridgev2.CreateChatResponse{
		PortalKey:  portal.PortalKey,
		Portal:     portal,
		PortalInfo: chatInfo,
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
		Slug: slug,
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
	var pmeta *PortalMetadata
	if opts.CopyFrom != nil {
		pmeta = cloneForkPortalMetadata(opts.CopyFrom, slug, title)
	} else {
		pmeta = &PortalMetadata{
			Slug: slug,
		}
	}
	chatInfo := oc.composeChatInfo(ctx, title, modelID)
	portal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to bootstrap portal: %w", err)
	}
	if err := bridgeutil.ConfigureAndPersistDMPortal(ctx, bridgeutil.ConfigureAndPersistDMPortalParams{
		Portal:      portal,
		Title:       title,
		OtherUserID: modelUserID(modelID),
		MutatePortal: func(portal *bridgev2.Portal) {
			portal.Metadata = pmeta
			setPortalResolvedTarget(portal, pmeta, modelUserID(modelID))
			defaultAvatar := strings.TrimSpace(agents.DefaultAgentAvatarMXC)
			if defaultAvatar != "" {
				portal.AvatarID = networkid.AvatarID(defaultAvatar)
				portal.AvatarMXC = id.ContentURIString(defaultAvatar)
			}
		},
		Persist: func(ctx context.Context, portal *bridgev2.Portal) error {
			return oc.savePortal(ctx, portal, "chat bootstrap")
		},
	}); err != nil {
		return nil, nil, fmt.Errorf("failed to bootstrap portal: %w", err)
	}
	if portal.MXID != "" {
		portal.UpdateInfo(ctx, chatInfo, oc.UserLogin, nil, time.Time{})
		portal.UpdateBridgeInfo(ctx)
		portal.UpdateCapabilities(ctx, oc.UserLogin, true)
	}
	oc.ensureGhostDisplayName(ctx, modelID)
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
	target, err := oc.resolveNewChatTarget(runCtx, meta, args)
	if err != nil {
		oc.sendSystemNotice(runCtx, portal, err.Error())
		return
	}
	if target == nil {
		oc.sendSystemNotice(runCtx, portal, "Couldn't create the chat: no target resolved")
		return
	}

	var (
		label  string
		params chatCreateParams
	)
	switch {
	case target.agent != nil:
		label = oc.resolveAgentDisplayName(runCtx, target.agent)
		params = chatCreateParams{
			ModelID: target.modelID,
			Agent:   target.agent,
		}
	case target.modelID != "":
		label = modelContactName(target.modelID, oc.findModelInfo(target.modelID))
		params = chatCreateParams{ModelID: target.modelID}
	default:
		oc.sendSystemNotice(runCtx, portal, "Couldn't create the chat: no target resolved")
		return
	}

	chatResp, err := oc.createChat(runCtx, params)
	if err != nil {
		oc.sendSystemNotice(runCtx, portal, "Couldn't create the chat: "+err.Error())
		return
	}

	newPortal, err := oc.ensurePortalRoom(runCtx, ensurePortalRoomParams{
		Portal:            chatResp.Portal,
		ChatInfo:          chatResp.PortalInfo,
		SendWelcomeNotice: true,
	})
	if err != nil {
		oc.sendSystemNotice(runCtx, portal, "Couldn't create the room: "+err.Error())
		return
	}

	roomLink := fmt.Sprintf("https://matrix.to/#/%s", newPortal.MXID)
	oc.sendSystemNotice(runCtx, portal, fmt.Sprintf(
		"New %s chat created.\nOpen: %s",
		label, roomLink,
	))
}

func (oc *AIClient) validateNewChatCommand(
	ctx context.Context,
	_ *bridgev2.Portal,
	meta *PortalMetadata,
	args []string,
) error {
	_, err := oc.resolveNewChatTarget(ctx, meta, args)
	return err
}

func (oc *AIClient) resolveNewChatTarget(
	ctx context.Context,
	meta *PortalMetadata,
	args []string,
) (*chatResolveTarget, error) {
	const usage = "usage: !ai new [agent <agent_id>]"
	agentID := ""
	preferredModel := ""

	if len(args) >= 2 {
		cmd := strings.ToLower(args[0])
		if cmd != "agent" {
			return nil, errors.New(usage)
		}
		if !oc.agentsEnabledForLogin() {
			return nil, agentChatsDisabledError()
		}
		targetID := args[1]
		if targetID == "" || len(args) > 2 {
			return nil, errors.New(usage)
		}
		agentID = targetID
	} else if len(args) == 1 {
		return nil, errors.New(usage)
	}

	if agentID == "" {
		if meta == nil {
			return nil, fmt.Errorf("couldn't resolve the current chat target")
		}
		agentID = resolveAgentID(meta)
		preferredModel = oc.effectiveModel(meta)
	}
	if agentID != "" {
		if !oc.agentsEnabledForLogin() {
			return nil, agentChatsDisabledError()
		}
		store := &AgentStoreAdapter{client: oc}
		agent, err := store.GetAgentByID(ctx, agentID)
		if err != nil || agent == nil {
			return nil, fmt.Errorf("agent not found: %s", agentID)
		}
		modelID, err := oc.resolveAgentModelForNewChat(ctx, agent, preferredModel)
		if err != nil {
			return nil, err
		}
		return &chatResolveTarget{agent: agent, modelID: modelID}, nil
	}

	modelID := oc.effectiveModel(meta)
	if modelID == "" {
		return nil, fmt.Errorf("no model configured for this room")
	}
	if ok, _ := oc.validateModel(ctx, modelID); !ok {
		return nil, fmt.Errorf("that model isn't available: %s", modelID)
	}
	return &chatResolveTarget{modelID: modelID}, nil
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

func (oc *AIClient) configureAgentChatPortal(
	ctx context.Context,
	portal *bridgev2.Portal,
	chatInfo *bridgev2.ChatInfo,
	agent *agents.AgentDefinition,
	modelID string,
	applyModelOverride bool,
	saveReason string,
) string {
	if oc == nil || portal == nil || agent == nil {
		return ""
	}
	agentName := oc.resolveAgentDisplayName(ctx, agent)
	agentGhostID := oc.agentUserID(agent.ID)
	pm := portalMeta(portal)
	setPortalResolvedTarget(portal, pm, agentGhostID)
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
	oc.savePortalQuiet(ctx, portal, saveReason)
	oc.ensureAgentGhostDisplayName(ctx, agent.ID, modelID, agentName)
	if chatInfo != nil {
		oc.applyAgentChatInfo(ctx, chatInfo, agent.ID, agentName, modelID)
	}
	return agentName
}

// chatInfoFromPortal builds ChatInfo from an existing portal
func (oc *AIClient) chatInfoFromPortal(ctx context.Context, portal *bridgev2.Portal) *bridgev2.ChatInfo {
	meta := portalMeta(portal)
	if meta != nil && meta.InternalRoom() {
		fallbackName := strings.TrimSpace(meta.Slug)
		if fallbackName == "" {
			fallbackName = "AI Chat"
		}
		if portal == nil {
			return nil
		}
		name := strings.TrimSpace(portal.Name)
		if name == "" {
			name = fallbackName
		}
		return &bridgev2.ChatInfo{
			Name:  ptr.Ptr(name),
			Topic: ptr.NonZero(strings.TrimSpace(portal.Topic)),
		}
	}
	modelID := oc.effectiveModel(meta)
	title := strings.TrimSpace(portal.Name)
	if title == "" {
		if slug := strings.TrimSpace(meta.Slug); slug != "" {
			title = slug
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
		store := &AgentStoreAdapter{client: oc}
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
	chatInfo := bridgeutil.BuildLoginDMChatInfo(bridgeutil.LoginDMChatInfoParams{
		Title:          title,
		Topic:          "",
		Login:          oc.UserLogin,
		HumanUserID:    humanUserID(oc.UserLogin.ID),
		BotUserID:      modelUserID(modelID),
		BotDisplayName: modelName,
		CanBackfill:    true,
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
	responder, err := oc.resolveResponder(ctx, &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{
			Kind:    ResolvedTargetAgent,
			AgentID: agentID,
		},
	}, ResponderResolveOptions{
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

func (oc *AIClient) sendSystemNoticeMessage(ctx context.Context, portal *bridgev2.Portal, message string) error {
	if oc == nil || oc.UserLogin == nil || portal == nil {
		return nil
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	portal, err := resolvePortalForAIDB(ctx, oc, portal)
	if err != nil {
		return err
	}
	if portal == nil || portal.MXID == "" {
		return fmt.Errorf("invalid portal")
	}
	return sdk.SendSystemMessage(ctx, oc.UserLogin, portal, oc.senderForPortal(ctx, portal), message)
}

// sendSystemNotice sends an informational notice through the canonical bridgev2
// portal sender path so it behaves like other bridges and like normal AI output.
func (oc *AIClient) sendSystemNotice(ctx context.Context, portal *bridgev2.Portal, message string) {
	if err := oc.sendSystemNoticeMessage(ctx, portal, message); err != nil {
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
	oc.loggerForContext(ctx).Info().Msg("Starting bootstrap for new login")

	if shouldEnsureDefaultChat(oc.loginConfigSnapshot(context.Background())) {
		// Create default chat room with Beep agent
		if err := oc.ensureDefaultChat(logCtx); err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to ensure default chat")
			return
		}
	}
	oc.loggerForContext(ctx).Info().Msg("Bootstrap completed successfully")
}

func (oc *AIClient) ensureDefaultChat(ctx context.Context) error {
	oc.loggerForContext(ctx).Debug().Msg("Ensuring default AI chat room exists")
	defaultPortalKey := defaultChatPortalKey(oc.UserLogin.ID)

	portal, err := oc.UserLogin.Bridge.GetExistingPortalByKey(ctx, defaultPortalKey)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to load default chat portal by deterministic key")
		return err
	}
	if portal != nil {
		if portal.MXID != "" {
			oc.loggerForContext(ctx).Debug().Stringer("portal", portal.PortalKey).Msg("Existing default chat already has MXID")
			return nil
		}
		oc.loggerForContext(ctx).Info().Stringer("portal", portal.PortalKey).Msg("Default chat missing MXID; creating Matrix room")
		if _, err := oc.ensurePortalRoom(ctx, ensurePortalRoomParams{Portal: portal, SendWelcomeNotice: true}); err != nil {
			oc.loggerForContext(ctx).Err(err).Msg("Failed to create Matrix room for default chat")
			return err
		}
		return nil
	}

	portals, err := oc.listAllChatPortals(ctx)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to list AI chat portals while ensuring default chat")
	} else if existing := chooseDefaultChatPortal(portals); existing != nil {
		if existing.MXID != "" {
			oc.loggerForContext(ctx).Debug().Stringer("portal", existing.PortalKey).Msg("Existing AI chat already has MXID")
			return nil
		}
		oc.loggerForContext(ctx).Info().Stringer("portal", existing.PortalKey).Msg("Existing AI chat missing MXID; creating Matrix room")
		if _, err := oc.ensurePortalRoom(ctx, ensurePortalRoomParams{Portal: existing, SendWelcomeNotice: true}); err != nil {
			oc.loggerForContext(ctx).Err(err).Msg("Failed to create Matrix room for existing AI chat")
			return err
		}
		return nil
	}

	// Create default chat with Beep agent
	beeperAgent := agents.GetBeeperAI()
	if beeperAgent == nil {
		return errors.New("beep agent not found")
	}

	// Determine model from agent config or use default
	modelID := beeperAgent.Model.Primary
	if modelID == "" {
		modelID = oc.effectiveModel(nil)
	}

	chatResp, err := oc.createChat(ctx, chatCreateParams{
		ModelID:   modelID,
		Agent:     beeperAgent,
		Title:     "New AI Chat",
		PortalKey: &defaultPortalKey,
	})
	if err != nil {
		oc.loggerForContext(ctx).Err(err).Msg("Failed to create default portal")
		return err
	}

	portal, err = oc.ensurePortalRoom(ctx, ensurePortalRoomParams{
		Portal:            chatResp.Portal,
		ChatInfo:          chatResp.PortalInfo,
		SendWelcomeNotice: true,
	})
	if err != nil {
		oc.loggerForContext(ctx).Err(err).Msg("Failed to create Matrix room for default chat")
		return err
	}
	oc.loggerForContext(ctx).Info().Stringer("portal", portal.PortalKey).Msg("New AI Chat room created")
	return nil
}

func (oc *AIClient) listAllChatPortals(ctx context.Context) ([]*bridgev2.Portal, error) {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return nil, nil
	}
	portals, err := oc.UserLogin.Bridge.GetAllPortals(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*bridgev2.Portal, 0, len(portals))
	for _, portal := range portals {
		if portal == nil || portal.Receiver != oc.UserLogin.ID {
			continue
		}
		if meta := portalMeta(portal); meta != nil {
			out = append(out, portal)
		}
	}
	return out, nil
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

// HandleMatrixMessageRemove keeps bridgev2 and the AI turn store in sync.
func (oc *AIClient) HandleMatrixMessageRemove(ctx context.Context, msg *bridgev2.MatrixMessageRemove) error {
	if oc == nil || msg == nil || msg.Portal == nil || msg.TargetMessage == nil {
		return nil
	}
	oc.loggerForContext(ctx).Debug().
		Stringer("event_id", msg.TargetMessage.MXID).
		Stringer("portal", msg.Portal.PortalKey).
		Msg("Handling message deletion")

	var errs []error
	if oc.UserLogin != nil && oc.UserLogin.Bridge != nil && oc.UserLogin.Bridge.DB != nil && oc.UserLogin.Bridge.DB.Message != nil && msg.TargetMessage.RowID != 0 {
		if err := oc.UserLogin.Bridge.DB.Message.Delete(ctx, msg.TargetMessage.RowID); err != nil {
			errs = append(errs, err)
		}
	}
	if err := oc.deleteAITurnByExternalRef(ctx, msg.Portal, msg.TargetMessage.ID, msg.TargetMessage.MXID); err != nil {
		errs = append(errs, err)
	}
	if meta := portalMeta(msg.Portal); meta != nil {
		oc.notifySessionMutation(ctx, msg.Portal, meta, true)
	}
	return errors.Join(errs...)
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
