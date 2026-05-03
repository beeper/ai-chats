package ai

import (
	"context"
	"strings"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

// buildAvailableTools returns a list of ToolInfo for all tools based on tool policy.

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

// SearchUsers searches available AI models by name/ID.
func (oc *AIClient) SearchUsers(ctx context.Context, query string) ([]*bridgev2.ResolveIdentifierResponse, error) {
	oc.loggerForContext(ctx).Debug().Str("query", query).Msg("Model search requested")
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

	oc.loggerForContext(ctx).Info().Str("query", query).Int("results", len(results)).Msg("Model search completed")
	return results, nil
}

// GetContactList returns available AI models as contacts.
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
