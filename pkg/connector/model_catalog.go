package connector

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/beeper/ai-bridge/pkg/textfs"
)

const (
	modelCatalogAgentID  = "__models__"
	modelCatalogStoreRef = "models.json"
	modelCatalogStoreAlt = "models/catalog.json"
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

func (oc *AIClient) modelCatalogStore() (*textfs.Store, error) {
	if oc == nil || oc.UserLogin == nil {
		return nil, nil
	}
	bridgeID := string(oc.UserLogin.Bridge.DB.BridgeID)
	loginID := string(oc.UserLogin.ID)
	agentID := normalizeAgentID(strings.TrimSpace(modelCatalogAgentID))
	return textfs.NewStore(oc.UserLogin.Bridge.DB.Database, bridgeID, loginID, agentID), nil
}

func (oc *AIClient) ensureModelCatalogVFS(ctx context.Context) (bool, error) {
	if oc == nil || oc.UserLogin == nil {
		return false, nil
	}
	loginMeta := loginMetadata(oc.UserLogin)
	if loginMeta == nil {
		return false, nil
	}

	implicit := oc.implicitModelCatalogEntries(loginMeta)
	explicit := explicitModelCatalogEntries(oc.connector.Config.Models)
	if len(implicit) == 0 && len(explicit) == 0 {
		return false, nil
	}

	mode := defaultModelCatalogMode
	if oc.connector.Config.Models != nil {
		if trimmed := strings.ToLower(strings.TrimSpace(oc.connector.Config.Models.Mode)); trimmed != "" {
			switch trimmed {
			case "replace":
				mode = "replace"
			case "merge":
				mode = defaultModelCatalogMode
			}
		}
	}

	var existing []ModelCatalogEntry
	if mode == defaultModelCatalogMode {
		existing = oc.loadModelCatalog(ctx, false)
	}

	merged := mergeCatalogEntries(existing, implicit, explicit)
	if len(merged) == 0 {
		return false, nil
	}

	payload := struct {
		Models []ModelCatalogEntry `json:"models"`
	}{
		Models: merged,
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return false, err
	}
	content := string(raw) + "\n"

	store, err := oc.modelCatalogStore()
	if err != nil || store == nil {
		return false, err
	}
	if entry, found, err := store.Read(ctx, modelCatalogStoreRef); err == nil && found {
		if entry.Content == content {
			return false, nil
		}
	}

	if _, err := store.Write(ctx, modelCatalogStoreRef, content); err != nil {
		return false, err
	}

	oc.modelCatalogMu.Lock()
	oc.modelCatalogLoaded = false
	oc.modelCatalogCache = nil
	oc.modelCatalogMu.Unlock()

	return true, nil
}

func mergeCatalogEntries(existing []ModelCatalogEntry, implicit []ModelCatalogEntry, explicit []ModelCatalogEntry) []ModelCatalogEntry {
	merged := map[string]ModelCatalogEntry{}
	for _, entry := range existing {
		if key := modelCatalogKey(entry.Provider, entry.ID); key != "" {
			merged[key] = entry
		}
	}
	for _, entry := range implicit {
		if key := modelCatalogKey(entry.Provider, entry.ID); key != "" {
			merged[key] = entry
		}
	}
	for _, entry := range explicit {
		if key := modelCatalogKey(entry.Provider, entry.ID); key != "" {
			merged[key] = entry
		}
	}

	out := make([]ModelCatalogEntry, 0, len(merged))
	for _, entry := range merged {
		out = append(out, entry)
	}
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
	switch meta.Provider {
	case ProviderOpenRouter:
		if strings.TrimSpace(oc.connector.resolveOpenRouterAPIKey(meta)) == "" {
			return nil
		}
		return modelCatalogEntriesFromManifest(nil)
	case ProviderBeeper:
		if strings.TrimSpace(oc.connector.resolveBeeperToken(meta)) == "" {
			return nil
		}
		return modelCatalogEntriesFromManifest(nil)
	case ProviderOpenAI:
		if strings.TrimSpace(oc.connector.resolveOpenAIAPIKey(meta)) == "" {
			return nil
		}
		return modelCatalogEntriesFromManifest(func(provider string) bool {
			return provider == ProviderOpenAI
		})
	default:
		return nil
	}
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
	out := make([]ModelCatalogEntry, 0)
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
			} else if providerID != ProviderOpenRouter && providerID != ProviderBeeper && providerID != ProviderMagicProxy {
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
	ordered := []string{}
	if seen["text"] {
		ordered = append(ordered, "text")
		delete(seen, "text")
	}
	if seen["image"] {
		ordered = append(ordered, "image")
		delete(seen, "image")
	}
	rest := make([]string, 0, len(seen))
	for key := range seen {
		rest = append(rest, key)
	}
	slices.Sort(rest)
	return append(ordered, rest...)
}

func (oc *AIClient) loadModelCatalog(ctx context.Context, useCache bool) []ModelCatalogEntry {
	if oc == nil || oc.UserLogin == nil {
		return nil
	}
	if useCache {
		oc.modelCatalogMu.Lock()
		if oc.modelCatalogLoaded {
			cached := append([]ModelCatalogEntry(nil), oc.modelCatalogCache...)
			oc.modelCatalogMu.Unlock()
			return cached
		}
		oc.modelCatalogMu.Unlock()
	}

	store, err := oc.modelCatalogStore()
	if err != nil || store == nil {
		return nil
	}
	entry, found, err := store.Read(ctx, modelCatalogStoreRef)
	if err != nil || !found {
		if modelCatalogStoreAlt != "" {
			entry, found, err = store.Read(ctx, modelCatalogStoreAlt)
		}
	}
	if err != nil || !found {
		if useCache {
			oc.modelCatalogMu.Lock()
			oc.modelCatalogLoaded = true
			oc.modelCatalogCache = nil
			oc.modelCatalogMu.Unlock()
		}
		return nil
	}

	var raw any
	if err := json.Unmarshal([]byte(entry.Content), &raw); err != nil {
		return nil
	}
	entries := parseModelCatalog(raw)
	if useCache {
		oc.modelCatalogMu.Lock()
		oc.modelCatalogLoaded = true
		oc.modelCatalogCache = append([]ModelCatalogEntry(nil), entries...)
		oc.modelCatalogMu.Unlock()
	}
	return entries
}

func parseModelCatalog(raw any) []ModelCatalogEntry {
	if raw == nil {
		return nil
	}
	switch value := raw.(type) {
	case []any:
		return coerceModelEntries(value)
	case map[string]any:
		if models, ok := value["models"].([]any); ok {
			return coerceModelEntries(models)
		}
	}
	return nil
}

func coerceModelEntries(items []any) []ModelCatalogEntry {
	out := make([]ModelCatalogEntry, 0, len(items))
	for _, item := range items {
		entryMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := strings.TrimSpace(asString(entryMap["id"]))
		provider := strings.TrimSpace(asString(entryMap["provider"]))
		if id == "" || provider == "" {
			continue
		}
		name := strings.TrimSpace(asString(entryMap["name"]))
		if name == "" {
			name = id
		}
		out = append(out, ModelCatalogEntry{
			ID:              id,
			Name:            name,
			Provider:        provider,
			ContextWindow:   asInt(entryMap["contextWindow"]),
			MaxOutputTokens: asInt(valueOr(entryMap, "maxTokens", "max_output_tokens")),
			Reasoning:       asBool(entryMap["reasoning"]),
			Input:           asStringSlice(entryMap["input"]),
		})
	}
	return out
}

func valueOr(m map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			return value
		}
	}
	return nil
}

func asString(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func asInt(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	default:
		return 0
	}
}

func asBool(value any) bool {
	if v, ok := value.(bool); ok {
		return v
	}
	return false
}

func asStringSlice(value any) []string {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if str, ok := item.(string); ok {
			out = append(out, str)
		}
	}
	return out
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
	if provider == ProviderOpenRouter || provider == ProviderBeeper || provider == ProviderMagicProxy {
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
