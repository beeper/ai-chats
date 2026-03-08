package openclaw

import (
	"context"
	"strings"
	"time"
)

const openClawMetadataCatalogTTL = 5 * time.Minute

func (oc *OpenClawClient) loadModelCatalog(ctx context.Context, force bool) ([]gatewayModelChoice, error) {
	now := time.Now()
	oc.modelCatalogMu.RLock()
	if !force && len(oc.modelCatalog) > 0 && now.Sub(oc.modelCatalogFetchedAt) < openClawMetadataCatalogTTL {
		cached := cloneGatewayModelChoices(oc.modelCatalog)
		oc.modelCatalogMu.RUnlock()
		return cached, nil
	}
	cached := cloneGatewayModelChoices(oc.modelCatalog)
	oc.modelCatalogMu.RUnlock()

	var gateway *gatewayWSClient
	if oc.manager != nil {
		gateway = oc.manager.gatewayClient()
	}
	if !oc.IsLoggedIn() || gateway == nil {
		return cached, nil
	}
	resp, err := gateway.ListModels(ctx)
	if err != nil {
		return cached, err
	}
	models := cloneGatewayModelChoices(resp.Models)
	oc.modelCatalogMu.Lock()
	oc.modelCatalog = cloneGatewayModelChoices(models)
	oc.modelCatalogFetchedAt = now
	oc.modelCatalogMu.Unlock()
	return models, nil
}

func (oc *OpenClawClient) loadToolsCatalog(ctx context.Context, agentID string, force bool) (*gatewayToolsCatalogResponse, error) {
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	if agentID == "" || strings.EqualFold(agentID, "gateway") {
		return nil, nil
	}
	now := time.Now()
	oc.toolCatalogMu.RLock()
	if !force {
		if cachedAt, ok := oc.toolCatalogFetchedAt[agentID]; ok && now.Sub(cachedAt) < openClawMetadataCatalogTTL {
			if cached, ok := oc.toolCatalog[agentID]; ok {
				cloned := cloneGatewayToolsCatalogResponse(cached)
				oc.toolCatalogMu.RUnlock()
				return &cloned, nil
			}
		}
	}
	var cached *gatewayToolsCatalogResponse
	if value, ok := oc.toolCatalog[agentID]; ok {
		cloned := cloneGatewayToolsCatalogResponse(value)
		cached = &cloned
	}
	oc.toolCatalogMu.RUnlock()

	var gateway *gatewayWSClient
	if oc.manager != nil {
		gateway = oc.manager.gatewayClient()
	}
	if !oc.IsLoggedIn() || gateway == nil {
		return cached, nil
	}
	resp, err := gateway.GetToolsCatalog(ctx, agentID)
	if err != nil {
		return cached, err
	}
	normalized := cloneGatewayToolsCatalogResponse(*resp)
	oc.toolCatalogMu.Lock()
	if oc.toolCatalog == nil {
		oc.toolCatalog = make(map[string]gatewayToolsCatalogResponse)
	}
	if oc.toolCatalogFetchedAt == nil {
		oc.toolCatalogFetchedAt = make(map[string]time.Time)
	}
	oc.toolCatalog[agentID] = normalized
	oc.toolCatalogFetchedAt[agentID] = now
	oc.toolCatalogMu.Unlock()
	return &normalized, nil
}

func (oc *OpenClawClient) enrichPortalMetadata(ctx context.Context, meta *PortalMetadata) {
	if oc == nil || meta == nil {
		return
	}
	oc.agentCatalogMu.RLock()
	defaultAgentID := strings.TrimSpace(oc.agentCatalogDefaultID)
	oc.agentCatalogMu.RUnlock()
	if defaultAgentID != "" && meta.OpenClawDefaultAgentID == "" {
		meta.OpenClawDefaultAgentID = defaultAgentID
	}
	if models, err := oc.loadModelCatalog(ctx, false); err == nil && len(models) > 0 {
		meta.OpenClawKnownModelCount = len(models)
	}
	agentID := stringsTrimDefault(meta.OpenClawAgentID, meta.OpenClawDMTargetAgentID)
	if catalog, err := oc.loadToolsCatalog(ctx, agentID, false); err == nil && catalog != nil {
		meta.OpenClawToolCount, meta.OpenClawToolProfile = summarizeToolsCatalog(*catalog)
	}
	if preview := strings.TrimSpace(meta.OpenClawLastMessagePreview); meta.OpenClawPreviewSnippet == "" && preview != "" {
		meta.OpenClawPreviewSnippet = preview
		if meta.OpenClawLastPreviewAt == 0 {
			meta.OpenClawLastPreviewAt = time.Now().UnixMilli()
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
	if err != nil || resp == nil {
		return ""
	}
	return previewSnippetForSession(*resp, sessionKey)
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
	cloned := make([]gatewayModelChoice, len(models))
	copy(cloned, models)
	return cloned
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
