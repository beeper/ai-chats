package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/beeper/agentremote/pkg/shared/bridgeutil"
	"github.com/beeper/agentremote/sdk"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func hasAssignedAgent(meta *PortalMetadata) bool {
	return false
}

func hasBossAgent(meta *PortalMetadata) bool {
	return false
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
	return false
}

func agentsEnabledForLoginConfig(cfg *aiLoginConfig) bool {
	return false
}

func agentChatsDisabledError() error {
	return bridgev2.WrapRespErr(errors.New("agent chats are disabled for this login"), mautrix.MForbidden)
}

// buildAvailableTools returns a list of ToolInfo for all tools based on tool policy.
func (oc *AIClient) buildAvailableTools(meta *PortalMetadata) []ToolInfo {
	names := oc.toolNamesForPortal(meta)
	var toolsList []ToolInfo

	for _, name := range names {
		displayName := name
		description := ""
		toolType := "builtin"
		description = oc.toolDescriptionForPortal(meta, name, description)

		available, source, reason := oc.isToolAvailable(meta, name)
		enabled := available

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
	return nil
}

func agentMatchesQuery(query string, agent *sdk.Agent) bool {
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

	results := make([]*bridgev2.ResolveIdentifierResponse, 0)
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
	return nil, bridgev2.WrapRespErr(fmt.Errorf("agent '%s' not found", strings.TrimSpace(agentID)), mautrix.MNotFound)
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
	case target.modelID != "":
		modelID := target.modelID
		userID := modelUserID(modelID)
		ghost, err := oc.resolveChatGhost(ctx, userID)
		if err != nil {
			return nil, err
		}

		oc.ensureGhostDisplayName(ctx, modelID)

		responder, err := oc.resolveResponder(ctx, &PortalMetadata{
			ResolvedTarget: &ResolvedTarget{
				Kind:    ResolvedTargetModel,
				ModelID: modelID,
			},
		}, ResponderResolveOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to resolve model responder: %w", err)
		}

		var chatResp *bridgev2.CreateChatResponse
		if createChat {
			oc.loggerForContext(ctx).Info().Str("model", modelID).Msg("Creating new chat")
			chatResp, err = oc.createChat(ctx, chatCreateParams{ModelID: modelID, SkipRoomCreation: true})
			if err != nil {
				return nil, fmt.Errorf("failed to create chat: %w", err)
			}
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
	ApplyModelOverride bool
	Title              string
	PortalKey          *networkid.PortalKey
	RoomName           string
	ParentRoomID       id.RoomID
	RuntimeReasoning   string
	SkipRoomCreation   bool
}

func (oc *AIClient) createChat(ctx context.Context, params chatCreateParams) (*bridgev2.CreateChatResponse, error) {
	modelID := strings.TrimSpace(params.ModelID)
	initOpts := PortalInitOpts{
		ModelID:   modelID,
		Title:     strings.TrimSpace(params.Title),
		PortalKey: params.PortalKey,
	}
	portal, chatInfo, err := oc.initPortalForChat(ctx, initOpts)
	if err != nil {
		return nil, err
	}
	roomName := strings.TrimSpace(params.RoomName)
	if roomName != "" {
		portal.Name = roomName
		portal.NameSet = true
		if chatInfo != nil {
			chatInfo.Name = &roomName
		}
	}
	meta := portalMeta(portal)
	if reasoning := strings.TrimSpace(params.RuntimeReasoning); reasoning != "" {
		meta.RuntimeReasoning = reasoning
	}
	if roomName != "" || strings.TrimSpace(params.RuntimeReasoning) != "" {
		if err := oc.savePortal(ctx, portal, "chat setup"); err != nil {
			return nil, fmt.Errorf("failed to save chat setup: %w", err)
		}
	}
	if !params.SkipRoomCreation {
		portal, err = oc.ensurePortalRoom(ctx, ensurePortalRoomParams{
			Portal:   portal,
			ChatInfo: chatInfo,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to materialize chat room: %w", err)
		}
		if err := oc.sendDisclaimerNotice(ctx, portal); err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Stringer("portal", portal.PortalKey).Msg("Failed to send initial disclaimer after chat creation")
		}
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
	portal.RoomType = database.RoomTypeDM
	portal.OtherUserID = modelUserID(modelID)
	portal.Name = strings.TrimSpace(title)
	portal.NameSet = portal.Name != ""
	portal.Topic = ""
	portal.TopicSet = false
	portal.Metadata = pmeta
	setPortalResolvedTarget(portal, pmeta, modelUserID(modelID))
	if err := oc.savePortal(ctx, portal, "chat bootstrap"); err != nil {
		return nil, nil, fmt.Errorf("failed to bootstrap portal: %w", err)
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

	var label string
	params := chatCreateParams{ModelID: target.modelID}
	if target.modelID != "" {
		label = modelContactName(target.modelID, oc.findModelInfo(target.modelID))
	} else {
		oc.sendSystemNotice(runCtx, portal, "Couldn't create the chat: no target resolved")
		return
	}

	chatResp, err := oc.createChat(runCtx, params)
	if err != nil {
		oc.sendSystemNotice(runCtx, portal, "Couldn't create the chat: "+err.Error())
		return
	}
	newPortal := chatResp.Portal

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
	if len(args) > 0 {
		return nil, errors.New("usage: !ai new")
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
	chatInfo := bridgeutil.BuildDMChatInfo(bridgeutil.DMChatInfoParams{
		Title:          title,
		Topic:          "",
		HumanUserID:    humanUserID(oc.UserLogin.ID),
		LoginID:        oc.UserLogin.ID,
		BotUserID:      modelUserID(modelID),
		BotDisplayName: modelName,
		CanBackfill:    false,
	})
	// Override bot member with model-specific UserInfo and extra fields.
	chatInfo.Members.MemberMap[modelUserID(modelID)] = oc.modelJoinMember(ctx, oc.UserLogin.ID, modelID, modelName, modelInfo)
	return chatInfo
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

// sendSystemNotice sends a bridge-authored notice via the shared SDK transport
// path instead of maintaining a bridge-local Matrix send implementation.
func (oc *AIClient) sendSystemNotice(ctx context.Context, portal *bridgev2.Portal, message string) {
	if err := oc.sendSystemNoticeMessage(ctx, portal, message); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to send system notice")
	}
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
