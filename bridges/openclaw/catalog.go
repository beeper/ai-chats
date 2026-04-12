package openclaw

import (
	"context"
	"strings"
	"time"

	"github.com/beeper/agentremote/pkg/shared/cachedvalue"
	"github.com/beeper/agentremote/pkg/shared/openclawconv"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

const openClawMetadataCatalogTTL = 5 * time.Minute

func (oc *OpenClawClient) loadModelCatalog(ctx context.Context, force bool) ([]gatewayModelChoice, error) {
	if oc.modelCache == nil {
		return nil, nil
	}
	return oc.modelCache.GetOrFetch(force, cloneGatewayModelChoices, func() ([]gatewayModelChoice, error) {
		var gateway *gatewayWSClient
		if oc.manager != nil {
			gateway = oc.manager.gatewayClient()
		}
		if !oc.IsLoggedIn() || gateway == nil {
			return nil, nil
		}
		resp, err := gateway.ListModels(ctx)
		if err != nil {
			return nil, err
		}
		return resp.Models, nil
	})
}

func (oc *OpenClawClient) loadToolsCatalog(ctx context.Context, agentID string, force bool) (*gatewayToolsCatalogResponse, error) {
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	if agentID == "" || strings.EqualFold(agentID, "gateway") {
		return nil, nil
	}
	cache := oc.getToolCache(agentID)
	result, err := cache.GetOrFetch(force, cloneGatewayToolsCatalogResponse, func() (gatewayToolsCatalogResponse, error) {
		var gateway *gatewayWSClient
		if oc.manager != nil {
			gateway = oc.manager.gatewayClient()
		}
		if !oc.IsLoggedIn() || gateway == nil {
			return gatewayToolsCatalogResponse{}, nil
		}
		resp, err := gateway.GetToolsCatalog(ctx, agentID)
		if err != nil {
			return gatewayToolsCatalogResponse{}, err
		}
		return *resp, nil
	})
	if err != nil {
		if result.AgentID != "" || len(result.Groups) > 0 {
			return &result, nil
		}
		return nil, err
	}
	if result.AgentID == "" && len(result.Groups) == 0 {
		return nil, nil
	}
	return &result, nil
}

func (oc *OpenClawClient) getToolCache(agentID string) *cachedvalue.CachedValue[gatewayToolsCatalogResponse] {
	oc.toolCacheMu.Lock()
	defer oc.toolCacheMu.Unlock()
	if oc.toolCaches == nil {
		oc.toolCaches = make(map[string]*cachedvalue.CachedValue[gatewayToolsCatalogResponse])
	}
	if c, ok := oc.toolCaches[agentID]; ok {
		return c
	}
	c := cachedvalue.New[gatewayToolsCatalogResponse](openClawMetadataCatalogTTL)
	oc.toolCaches[agentID] = c
	return c
}

// agentDefaultID returns the default agent ID from the agent catalog cache.
func (oc *OpenClawClient) agentDefaultID() string {
	if oc.agentCache == nil {
		return ""
	}
	entry := oc.agentCache.Read(func(e agentCatalogEntry) agentCatalogEntry { return e })
	return strings.TrimSpace(entry.DefaultID)
}

func (oc *OpenClawClient) enrichPortalState(ctx context.Context, state *openClawPortalState) {
	if oc == nil || state == nil {
		return
	}
	state.OpenClawDefaultAgentID = ""
	state.OpenClawKnownModelCount = 0
	state.OpenClawToolCount = 0
	state.OpenClawToolProfile = ""
	defaultAgentID := oc.agentDefaultID()
	if defaultAgentID != "" {
		state.OpenClawDefaultAgentID = defaultAgentID
	}
	if models, err := oc.loadModelCatalog(ctx, false); err == nil && len(models) > 0 {
		state.OpenClawKnownModelCount = len(models)
	}
	agentID := stringutil.TrimDefault(state.OpenClawAgentID, state.OpenClawDMTargetAgentID)
	if catalog, err := oc.loadToolsCatalog(ctx, agentID, false); err == nil && catalog != nil {
		state.OpenClawToolCount, state.OpenClawToolProfile = summarizeToolsCatalog(*catalog)
	}
	if preview := strings.TrimSpace(state.OpenClawLastMessagePreview); state.OpenClawPreviewSnippet == "" && preview != "" {
		state.OpenClawPreviewSnippet = preview
		if state.OpenClawLastPreviewAt == 0 {
			state.OpenClawLastPreviewAt = time.Now().UnixMilli()
		}
	}
}

func (oc *OpenClawClient) previewSessionSnippet(ctx context.Context, sessionKey string) string {
	if oc == nil || oc.manager == nil {
		return ""
	}
	gateway := oc.manager.gatewayClient()
	if gateway == nil {
		return ""
	}
	resp, err := gateway.PreviewSessions(ctx, []string{sessionKey}, 6, 240)
	if err == nil && resp != nil {
		if snippet := previewSnippetForSession(*resp, sessionKey); snippet != "" {
			return snippet
		}
	}
	history, err := gateway.SessionHistory(ctx, sessionKey, 6, "")
	if err != nil || history == nil {
		return ""
	}
	return previewSnippetFromHistory(history.Messages)
}

func previewSnippetForSession(resp gatewaySessionsPreviewResponse, sessionKey string) string {
	for _, preview := range resp.Previews {
		if strings.TrimSpace(preview.Key) != strings.TrimSpace(sessionKey) {
			continue
		}
		var parts []string
		for _, item := range preview.Items {
			text := strings.TrimSpace(item.Text)
			if text == "" {
				continue
			}
			parts = append(parts, text)
		}
		return strings.TrimSpace(strings.Join(parts, " "))
	}
	return ""
}

func previewSnippetFromHistory(messages []map[string]any) string {
	var parts []string
	for _, message := range messages {
		text := strings.TrimSpace(openclawconv.ExtractMessageText(message))
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func summarizeToolsCatalog(resp gatewayToolsCatalogResponse) (int, string) {
	count := 0
	for _, group := range resp.Groups {
		count += len(group.Tools)
	}
	profile := ""
	if len(resp.Profiles) > 0 {
		profile = strings.TrimSpace(resp.Profiles[0].Label)
		if profile == "" {
			profile = strings.TrimSpace(resp.Profiles[0].ID)
		}
	}
	return count, profile
}

func cloneGatewayModelChoices(models []gatewayModelChoice) []gatewayModelChoice {
	if models == nil {
		return nil
	}
	cloned := make([]gatewayModelChoice, len(models))
	for i := range models {
		cloned[i] = models[i]
		if len(models[i].Input) > 0 {
			cloned[i].Input = append([]string(nil), models[i].Input...)
		}
	}
	return cloned
}

func (oc *OpenClawClient) effectiveModelChoice(ctx context.Context, state *openClawPortalState) *gatewayModelChoice {
	if oc == nil || state == nil {
		return nil
	}
	modelID := strings.TrimSpace(state.Model)
	if modelID == "" {
		return nil
	}
	models, err := oc.loadModelCatalog(ctx, false)
	if err != nil || len(models) == 0 {
		return nil
	}
	provider := strings.TrimSpace(state.ModelProvider)
	var fallback *gatewayModelChoice
	for i := range models {
		if !gatewayModelMatches(models[i], modelID) {
			continue
		}
		model := models[i]
		if provider == "" || strings.EqualFold(strings.TrimSpace(model.Provider), provider) {
			return &model
		}
		if fallback == nil {
			fallback = &model
		}
	}
	return fallback
}

func gatewayModelMatches(model gatewayModelChoice, query string) bool {
	query = strings.TrimSpace(query)
	if query == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(model.ID), query) ||
		strings.EqualFold(strings.TrimSpace(model.Name), query)
}

func cloneGatewayToolsCatalogResponse(resp gatewayToolsCatalogResponse) gatewayToolsCatalogResponse {
	cloned := gatewayToolsCatalogResponse{
		AgentID:  strings.TrimSpace(resp.AgentID),
		Profiles: make([]gatewayToolCatalogProfile, len(resp.Profiles)),
		Groups:   make([]gatewayToolCatalogGroup, len(resp.Groups)),
	}
	copy(cloned.Profiles, resp.Profiles)
	for i := range resp.Groups {
		cloned.Groups[i] = resp.Groups[i]
		cloned.Groups[i].Tools = make([]gatewayToolCatalogEntry, len(resp.Groups[i].Tools))
		copy(cloned.Groups[i].Tools, resp.Groups[i].Tools)
	}
	return cloned
}
