package ai

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strings"
)

const defaultModelCatalogMode = "merge"

type ModelCatalogEntry struct {
	ID              string   `json:"id"`
	Name            string   `json:"name,omitempty"`
	Provider        string   `json:"provider"`
	ContextWindow   int      `json:"contextWindow,omitempty"`
	MaxOutputTokens int      `json:"maxTokens,omitempty"`
	Reasoning       bool     `json:"reasoning,omitempty"`
	Input           []string `json:"input,omitempty"`
}

func mergeCatalogEntries(existing []ModelCatalogEntry, implicit []ModelCatalogEntry, explicit []ModelCatalogEntry) []ModelCatalogEntry {
	merged := map[string]ModelCatalogEntry{}
	// Later slices override earlier ones (explicit > implicit > existing).
	for _, entries := range [][]ModelCatalogEntry{existing, implicit, explicit} {
		for _, entry := range entries {
			if key := modelCatalogKey(entry.Provider, entry.ID); key != "" {
				merged[key] = entry
			}
		}
	}

	out := slices.Collect(maps.Values(merged))
	slices.SortFunc(out, func(a, b ModelCatalogEntry) int {
		if c := cmp.Compare(a.Provider, b.Provider); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out
}

func modelCatalogKey(provider string, id string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	m := strings.ToLower(strings.TrimSpace(id))
	if p == "" || m == "" {
		return ""
	}
	return p + "::" + m
}

func (oc *AIClient) implicitModelCatalogEntries(meta *UserLoginMetadata) []ModelCatalogEntry {
	if meta == nil {
		return nil
	}

	// Resolve the relevant API key for the provider.
	var apiKey string
	switch meta.Provider {
	case ProviderMagicProxy, ProviderOpenRouter:
		apiKey = oc.connector.resolveOpenRouterAPIKey(meta)
	case ProviderOpenAI:
		apiKey = oc.connector.resolveOpenAIAPIKey(meta)
	default:
		return nil
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil
	}

	// OpenAI-only logins see a filtered manifest; multi-provider logins see all models.
	if meta.Provider == ProviderOpenAI {
		return modelCatalogEntriesFromManifest(func(provider string) bool {
			return provider == ProviderOpenAI
		})
	}
	return modelCatalogEntriesFromManifest(nil)
}

func modelCatalogEntriesFromManifest(filter func(provider string) bool) []ModelCatalogEntry {
	out := make([]ModelCatalogEntry, 0, len(ModelManifest.Models))
	for _, info := range ModelManifest.Models {
		provider, modelID := splitModelProvider(info.ID)
		if provider == "" || modelID == "" {
			continue
		}
		if filter != nil && !filter(provider) {
			continue
		}
		name := strings.TrimSpace(info.Name)
		if name == "" {
			name = modelID
		}
		entry := ModelCatalogEntry{
			ID:              modelID,
			Name:            name,
			Provider:        provider,
			ContextWindow:   info.ContextWindow,
			MaxOutputTokens: info.MaxOutputTokens,
			Reasoning:       info.SupportsReasoning,
			Input: normalizeCatalogInput(nil, map[string]bool{
				"image": info.SupportsVision,
				"audio": info.SupportsAudio,
				"video": info.SupportsVideo,
				"pdf":   info.SupportsPDF,
			}),
		}
		out = append(out, entry)
	}
	return out
}

func explicitModelCatalogEntries(cfg *ModelsConfig) []ModelCatalogEntry {
	if cfg == nil || len(cfg.Providers) == 0 {
		return nil
	}
	var out []ModelCatalogEntry
	for providerKey, provider := range cfg.Providers {
		baseProviderID := strings.ToLower(strings.TrimSpace(providerKey))
		for _, model := range provider.Models {
			providerID := baseProviderID
			id := strings.TrimSpace(model.ID)
			if id == "" {
				continue
			}
			if providerID == "" {
				if parsedProvider, parsedID := splitModelProvider(id); parsedProvider != "" && parsedID != "" {
					providerID = parsedProvider
					id = parsedID
				}
			} else if providerID != ProviderOpenRouter && providerID != ProviderMagicProxy {
				if parsedProvider, parsedID := splitModelProvider(id); parsedProvider != "" && parsedID != "" && parsedProvider == providerID {
					id = parsedID
				}
			}
			if providerID == "" || id == "" {
				continue
			}
			name := strings.TrimSpace(model.Name)
			if name == "" {
				name = id
			}
			out = append(out, ModelCatalogEntry{
				ID:              id,
				Name:            name,
				Provider:        providerID,
				ContextWindow:   model.ContextWindow,
				MaxOutputTokens: model.MaxTokens,
				Reasoning:       model.Reasoning,
				Input:           normalizeCatalogInput(model.Input, nil),
			})
		}
	}
	return out
}

func normalizeCatalogInput(input []string, extra map[string]bool) []string {
	seen := map[string]bool{}
	add := func(value string) {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" || seen[normalized] {
			return
		}
		seen[normalized] = true
	}
	add("text")
	for _, value := range input {
		add(value)
	}
	for key, enabled := range extra {
		if enabled {
			add(key)
		}
	}
	if len(seen) == 0 {
		return nil
	}
	var ordered []string
	if seen["text"] {
		ordered = append(ordered, "text")
		delete(seen, "text")
	}
	if seen["image"] {
		ordered = append(ordered, "image")
		delete(seen, "image")
	}
	rest := slices.Sorted(maps.Keys(seen))
	return append(ordered, rest...)
}

func (oc *AIClient) loadModelCatalog(ctx context.Context, useCache bool) []ModelCatalogEntry {
	if oc == nil || oc.UserLogin == nil {
		return nil
	}
	if useCache {
		oc.modelCatalogMu.Lock()
		if oc.modelCatalogLoaded {
			cached := slices.Clone(oc.modelCatalogCache)
			oc.modelCatalogMu.Unlock()
			return cached
		}
		oc.modelCatalogMu.Unlock()
	}

	entries := oc.derivedModelCatalogEntries()
	if useCache {
		oc.modelCatalogMu.Lock()
		oc.modelCatalogLoaded = true
		oc.modelCatalogCache = slices.Clone(entries)
		oc.modelCatalogMu.Unlock()
	}
	return entries
}

func (oc *AIClient) derivedModelCatalogEntries() []ModelCatalogEntry {
	if oc == nil || oc.UserLogin == nil || oc.connector == nil {
		return nil
	}
	loginMeta := loginMetadata(oc.UserLogin)
	if loginMeta == nil {
		return nil
	}

	implicit := oc.implicitModelCatalogEntries(loginMeta)
	explicit := explicitModelCatalogEntries(oc.connector.Config.Models)
	mode := defaultModelCatalogMode
	if oc.connector != nil && oc.connector.Config.Models != nil {
		switch strings.ToLower(strings.TrimSpace(oc.connector.Config.Models.Mode)) {
		case "replace":
			mode = "replace"
		}
	}

	if mode == "replace" {
		return mergeCatalogEntries(nil, nil, explicit)
	}
	return mergeCatalogEntries(nil, implicit, explicit)
}

func catalogInputIncludes(entry *ModelCatalogEntry, label string) bool {
	if entry == nil || label == "" {
		return false
	}
	for _, input := range entry.Input {
		if strings.EqualFold(input, label) {
			return true
		}
	}
	return false
}

func findModelCatalogEntry(catalog []ModelCatalogEntry, provider string, model string) *ModelCatalogEntry {
	if provider == "" || model == "" {
		return nil
	}
	needleProvider := strings.ToLower(strings.TrimSpace(provider))
	needleModel := strings.ToLower(strings.TrimSpace(model))
	for i := range catalog {
		entry := &catalog[i]
		if strings.ToLower(entry.Provider) == needleProvider && strings.ToLower(entry.ID) == needleModel {
			return entry
		}
	}
	return nil
}

func modelCatalogSupportsVision(entry *ModelCatalogEntry) bool {
	return catalogInputIncludes(entry, "image")
}

func (oc *AIClient) modelSupportsVision(ctx context.Context, meta *PortalMetadata) bool {
	if oc == nil || meta == nil {
		return false
	}
	modelID := strings.TrimSpace(oc.effectiveModel(meta))
	if modelID == "" {
		return false
	}
	caps := getModelCapabilities(modelID, oc.findModelInfo(modelID))
	if caps.SupportsVision {
		return true
	}
	catalog := oc.loadModelCatalog(ctx, true)
	if len(catalog) == 0 {
		return false
	}
	provider, model := splitModelProvider(modelID)
	if provider == "" {
		provider = normalizeMediaProviderID(loginMetadata(oc.UserLogin).Provider)
	}
	if provider == "" {
		return false
	}
	entry := findModelCatalogEntry(catalog, provider, model)
	return modelCatalogSupportsVision(entry)
}

func normalizeCatalogProvider(provider string) string {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	switch normalized {
	case ProviderMagicProxy:
		return ProviderOpenRouter
	default:
		return normalized
	}
}

func normalizeCatalogModelID(entry ModelCatalogEntry) string {
	id := strings.TrimSpace(entry.ID)
	if id == "" {
		return ""
	}
	if strings.Contains(id, "/") {
		return id
	}
	provider := normalizeCatalogProvider(entry.Provider)
	if provider == ProviderOpenAI {
		return ProviderOpenAI + "/" + id
	}
	if provider == ProviderOpenRouter || provider == ProviderMagicProxy {
		return id
	}
	if provider != "" {
		return provider + "/" + id
	}
	return id
}

func (oc *AIClient) loadModelCatalogModels(ctx context.Context) []ModelInfo {
	entries := oc.loadModelCatalog(ctx, true)
	if len(entries) == 0 {
		return nil
	}
	models := make([]ModelInfo, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.ID) == "" || strings.TrimSpace(entry.Provider) == "" {
			continue
		}
		normalizedID := normalizeCatalogModelID(entry)
		if normalizedID == "" {
			continue
		}
		provider := normalizeCatalogProvider(entry.Provider)
		info := ModelInfo{
			ID:                  normalizedID,
			Name:                strings.TrimSpace(entry.Name),
			Provider:            provider,
			SupportsVision:      catalogInputIncludes(&entry, "image"),
			SupportsAudio:       catalogInputIncludes(&entry, "audio"),
			SupportsVideo:       catalogInputIncludes(&entry, "video"),
			SupportsPDF:         catalogInputIncludes(&entry, "pdf"),
			SupportsToolCalling: true,
			SupportsReasoning:   entry.Reasoning,
			ContextWindow:       entry.ContextWindow,
			MaxOutputTokens:     entry.MaxOutputTokens,
		}
		if info.Name == "" {
			info.Name = normalizedID
		}
		models = append(models, info)
	}
	return models
}

func (oc *AIClient) findModelInfoInCatalog(modelID string) *ModelInfo {
	if oc == nil || strings.TrimSpace(modelID) == "" {
		return nil
	}
	ctx := oc.backgroundContext(context.Background())
	entries := oc.loadModelCatalog(ctx, true)
	if len(entries) == 0 {
		return nil
	}
	normalizedTarget := strings.TrimSpace(modelID)
	for _, entry := range entries {
		if strings.TrimSpace(entry.ID) == "" || strings.TrimSpace(entry.Provider) == "" {
			continue
		}
		normalizedID := normalizeCatalogModelID(entry)
		if strings.EqualFold(normalizedTarget, normalizedID) ||
			strings.EqualFold(normalizedTarget, entry.ID) {
			info := ModelInfo{
				ID:                  normalizedID,
				Name:                strings.TrimSpace(entry.Name),
				Provider:            normalizeCatalogProvider(entry.Provider),
				SupportsVision:      catalogInputIncludes(&entry, "image"),
				SupportsAudio:       catalogInputIncludes(&entry, "audio"),
				SupportsVideo:       catalogInputIncludes(&entry, "video"),
				SupportsPDF:         catalogInputIncludes(&entry, "pdf"),
				SupportsToolCalling: true,
				SupportsReasoning:   entry.Reasoning,
				ContextWindow:       entry.ContextWindow,
				MaxOutputTokens:     entry.MaxOutputTokens,
			}
			if info.Name == "" {
				info.Name = normalizedID
			}
			return &info
		}
	}
	return nil
}
