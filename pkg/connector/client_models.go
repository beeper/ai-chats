package connector

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

// effectiveModel returns the full prefixed model ID (e.g., "openai/gpt-5.2")
// based only on the resolved room target.
func (oc *AIClient) effectiveModel(meta *PortalMetadata) string {
	responder := oc.responderForMeta(context.Background(), meta)
	if responder == nil {
		return ""
	}
	return responder.ModelID
}

// effectiveModelForAPI returns the actual model name to send to the API
// For OpenRouter/Beeper, returns the full model ID (e.g., "openai/gpt-5.2")
// For direct providers, strips the prefix (e.g., "openai/gpt-5.2" → "gpt-5.2")

// modelIDForAPI converts a full model ID to the provider-specific API model name.
// For OpenRouter-compatible providers, returns the full model ID.
// For direct providers, strips the prefix (e.g., "openai/gpt-5.2" → "gpt-5.2").
func (oc *AIClient) modelIDForAPI(modelID string) string {
	if oc.isOpenRouterProvider() {
		return modelID
	}
	_, actualModel := ParseModelPrefix(modelID)
	return actualModel
}

// defaultModelForProvider returns the configured default model for this login's provider
func (oc *AIClient) defaultModelForProvider() string {
	if oc == nil || oc.connector == nil || oc.UserLogin == nil {
		return DefaultModelOpenRouter
	}
	switch loginMetadata(oc.UserLogin).Provider {
	case ProviderOpenAI:
		return oc.defaultModelSelection(ProviderOpenAI).Primary
	case ProviderOpenRouter, ProviderMagicProxy:
		return oc.defaultModelSelection(ProviderOpenRouter).Primary
	default:
		return DefaultModelOpenRouter
	}
}

func (oc *AIClient) defaultModelSelection(provider string) ModelSelectionConfig {
	return ModelSelectionConfig{Primary: defaultModelForProviderName(provider)}
}

func defaultModelForProviderName(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case ProviderOpenAI:
		return DefaultModelOpenAI
	case ProviderOpenRouter, ProviderMagicProxy:
		return DefaultModelOpenRouter
	default:
		return DefaultModelOpenRouter
	}
}

// effectivePrompt returns the base system prompt.
func (oc *AIClient) effectivePrompt(meta *PortalMetadata) string {
	base := oc.connector.Config.DefaultSystemPrompt
	supplement := oc.profilePromptSupplement()
	if supplement == "" {
		return base
	}
	if strings.TrimSpace(base) == "" {
		return supplement
	}
	return fmt.Sprintf("%s\n\n%s", base, supplement)
}

func (oc *AIClient) profilePromptSupplement() string {
	if oc == nil || oc.UserLogin == nil {
		return strings.TrimSpace(oc.gravatarContext())
	}
	loginCfg := oc.loginConfigSnapshot(context.Background())

	var lines []string
	if profile := loginCfg.Profile; profile != nil {
		if v := strings.TrimSpace(profile.Name); v != "" {
			lines = append(lines, "Name: "+v)
		}
		if v := strings.TrimSpace(profile.Occupation); v != "" {
			lines = append(lines, "Occupation: "+v)
		}
		if v := strings.TrimSpace(profile.AboutUser); v != "" {
			lines = append(lines, "About the user: "+v)
		}
		if v := strings.TrimSpace(profile.CustomInstructions); v != "" {
			lines = append(lines, "Custom instructions: "+v)
		}
	}
	if gravatar := strings.TrimSpace(oc.gravatarContext()); gravatar != "" {
		lines = append(lines, gravatar)
	}
	if len(lines) == 0 {
		return ""
	}
	return "User profile:\n- " + strings.Join(lines, "\n- ")
}

// getLinkPreviewConfig returns the link preview configuration, with defaults filled in.
func getLinkPreviewConfig(connectorConfig *Config) LinkPreviewConfig {
	config := DefaultLinkPreviewConfig()

	if connectorConfig.Tools.Links != nil {
		cfg := connectorConfig.Tools.Links
		// Apply explicit settings only if they differ from zero values
		if !cfg.Enabled {
			config.Enabled = cfg.Enabled
		}
		if cfg.MaxURLsInbound > 0 {
			config.MaxURLsInbound = cfg.MaxURLsInbound
		}
		if cfg.MaxURLsOutbound > 0 {
			config.MaxURLsOutbound = cfg.MaxURLsOutbound
		}
		if cfg.FetchTimeout > 0 {
			config.FetchTimeout = cfg.FetchTimeout
		}
		if cfg.MaxContentChars > 0 {
			config.MaxContentChars = cfg.MaxContentChars
		}
		if cfg.MaxPageBytes > 0 {
			config.MaxPageBytes = cfg.MaxPageBytes
		}
		if cfg.MaxImageBytes > 0 {
			config.MaxImageBytes = cfg.MaxImageBytes
		}
		if cfg.CacheTTL > 0 {
			config.CacheTTL = cfg.CacheTTL
		}
	}

	return config
}

func (oc *AIClient) historyLimit(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) int {
	isGroup := portal != nil && oc.isGroupChat(ctx, portal)
	if oc != nil && oc.connector != nil && oc.connector.Config.Messages != nil {
		if isGroup {
			if cfg := oc.connector.Config.Messages.GroupChat; cfg != nil && cfg.HistoryLimit >= 0 {
				return cfg.HistoryLimit
			}
			return defaultGroupContextMessages
		}
		if cfg := oc.connector.Config.Messages.DirectChat; cfg != nil && cfg.HistoryLimit >= 0 {
			return cfg.HistoryLimit
		}
	}
	if isGroup {
		return defaultGroupContextMessages
	}
	return defaultMaxContextMessages
}

// isOpenRouterProvider checks if the current provider uses the OpenRouter-compatible API surface.
func (oc *AIClient) isOpenRouterProvider() bool {
	provider := loginMetadata(oc.UserLogin).Provider
	return provider == ProviderOpenRouter || provider == ProviderMagicProxy
}

// isGroupChat determines if the portal is a group chat.
// Prefer explicit portal metadata over member count to avoid misclassifying DMs
// that include extra ghosts (e.g. AI model users).
func (oc *AIClient) isGroupChat(ctx context.Context, portal *bridgev2.Portal) bool {
	if portal == nil || portal.MXID == "" {
		return false
	}

	switch portal.RoomType {
	case database.RoomTypeDM:
		return false
	case database.RoomTypeGroupDM, database.RoomTypeSpace:
		return true
	}
	if portal.OtherUserID != "" {
		return false
	}

	// Fallback to member count when portal type is unknown.
	matrixConn := oc.UserLogin.Bridge.Matrix
	if matrixConn == nil {
		return false
	}
	members, err := matrixConn.GetMembers(ctx, portal.MXID)
	if err != nil {
		oc.loggerForContext(ctx).Debug().Err(err).Msg("Failed to get joined members for group chat detection")
		return false
	}

	// Group chat = more than 2 members (user + bot = 1:1, user + bot + others = group)
	return len(members) > 2
}

func (oc *AIClient) defaultPDFEngine() string {
	if oc != nil && oc.connector != nil {
		return oc.connector.defaultPDFEngineForInit()
	}
	return "mistral-ocr"
}

// effectivePDFEngine returns the PDF engine to use for the given portal.
func (oc *AIClient) effectivePDFEngine(meta *PortalMetadata) string {
	// Room-level override
	if meta != nil && meta.PDFConfig != nil && meta.PDFConfig.Engine != "" {
		return meta.PDFConfig.Engine
	}
	return oc.defaultPDFEngine()
}

func candidateModelLookupIDs(modelID string) []string {
	normalized := strings.TrimSpace(modelID)
	if normalized == "" {
		return nil
	}
	candidates := []string{normalized}
	decoded, err := url.PathUnescape(normalized)
	if err == nil {
		decoded = strings.TrimSpace(decoded)
		if decoded != "" && decoded != normalized {
			candidates = append(candidates, decoded)
		}
	}
	return candidates
}

// resolveModelID validates canonical model IDs only (hard-cut mode).
func (oc *AIClient) resolveModelID(ctx context.Context, modelID string) (string, bool, error) {
	candidates := candidateModelLookupIDs(modelID)
	if len(candidates) == 0 {
		return "", true, nil
	}

	models, err := oc.listAvailableModels(ctx, false)
	if err == nil && len(models) > 0 {
		for _, candidate := range candidates {
			for _, model := range models {
				if model.ID == candidate {
					return model.ID, true, nil
				}
			}
		}
	}

	for _, candidate := range candidates {
		if fallback := resolveModelIDFromManifest(candidate); fallback != "" {
			return fallback, true, nil
		}
	}

	return "", false, nil
}

func resolveModelIDFromManifest(modelID string) string {
	normalized := strings.TrimSpace(modelID)
	if normalized == "" {
		return ""
	}

	if _, ok := ModelManifest.Models[normalized]; ok {
		return normalized
	}
	return ""
}

// listAvailableModels loads models from the derived catalog and caches them.
// The implicit catalog is fed from the OpenRouter-backed manifest.
func (oc *AIClient) listAvailableModels(ctx context.Context, forceRefresh bool) ([]ModelInfo, error) {
	state := oc.loginStateSnapshot(ctx)

	// Check cache (refresh every 6 hours unless forced)
	if !forceRefresh && state.ModelCache != nil {
		age := time.Now().Unix() - state.ModelCache.LastRefresh
		if age < state.ModelCache.CacheDuration {
			return state.ModelCache.Models, nil
		}
	}

	oc.loggerForContext(ctx).Debug().Msg("Loading derived model catalog")
	allModels := oc.loadModelCatalogModels(ctx)

	if err := oc.updateLoginState(ctx, func(state *loginRuntimeState) bool {
		if state.ModelCache == nil {
			state.ModelCache = &ModelCache{
				CacheDuration: int64(oc.connector.Config.ModelCacheDuration.Seconds()),
			}
		}
		state.ModelCache.Models = allModels
		state.ModelCache.LastRefresh = time.Now().Unix()
		if state.ModelCache.CacheDuration == 0 {
			state.ModelCache.CacheDuration = int64(oc.connector.Config.ModelCacheDuration.Seconds())
		}
		return true
	}); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to save model cache")
	}

	oc.loggerForContext(ctx).Info().Int("count", len(allModels)).Msg("Cached available models")
	return allModels, nil
}

// findModelInfo looks up ModelInfo from the user's model cache by ID
func (oc *AIClient) findModelInfo(modelID string) *ModelInfo {
	if info, ok := ModelManifest.Models[strings.TrimSpace(modelID)]; ok {
		copy := info
		return &copy
	}
	state := oc.loginStateSnapshot(context.Background())
	if state != nil && state.ModelCache != nil {
		for i := range state.ModelCache.Models {
			if state.ModelCache.Models[i].ID == modelID {
				return &state.ModelCache.Models[i]
			}
		}
	}
	return oc.findModelInfoInCatalog(modelID)
}
