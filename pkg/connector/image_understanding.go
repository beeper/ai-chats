package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/event"
)

const (
	defaultImageUnderstandingPrompt = "Describe the image."
	defaultAudioUnderstandingPrompt = "Transcribe the audio."
	imageUnderstandingMaxTokens     = 1024
)

func (oc *AIClient) canUseMediaUnderstanding(meta *PortalMetadata) bool {
	if meta == nil || meta.IsRawMode {
		return false
	}
	return hasAssignedAgent(meta)
}

type modelCapsFilter func(ModelCapabilities) bool
type modelInfoFilter func(ModelInfo) bool

func collectModelCandidates(primary string, fallbacks []string) []string {
	var candidates []string
	if strings.TrimSpace(primary) != "" {
		candidates = append(candidates, primary)
	}
	for _, fb := range fallbacks {
		if strings.TrimSpace(fb) == "" {
			continue
		}
		candidates = append(candidates, fb)
	}
	return candidates
}

func (oc *AIClient) resolveUnderstandingModel(
	ctx context.Context,
	meta *PortalMetadata,
	supportsCaps modelCapsFilter,
	supportsInfo modelInfoFilter,
	logLabel string,
) string {
	if !oc.canUseMediaUnderstanding(meta) {
		return ""
	}

	agentID := resolveAgentID(meta)
	if agentID == "" {
		return ""
	}

	store := NewAgentStoreAdapter(oc)
	agent, err := store.GetAgentByID(ctx, agentID)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Str("agent_id", agentID).Msg(fmt.Sprintf("Failed to load agent for %s understanding", logLabel))
		return ""
	}
	if agent == nil {
		return ""
	}

	candidates := collectModelCandidates(agent.Model.Primary, agent.Model.Fallbacks)
	for _, candidate := range candidates {
		resolved := ResolveAlias(candidate)
		if resolved == "" {
			continue
		}
		caps := getModelCapabilities(resolved, oc.findModelInfo(resolved))
		if supportsCaps(caps) {
			return resolved
		}
	}

	loginMeta := loginMetadata(oc.UserLogin)
	provider := loginMeta.Provider

	// Prefer cached/provider-listed models first.
	if modelID := oc.pickModelFromCache(loginMeta.ModelCache, provider, supportsInfo); modelID != "" {
		return modelID
	}
	models, err := oc.listAvailableModels(ctx, false)
	if err == nil {
		if modelID := pickModelFromList(models, provider, supportsInfo); modelID != "" {
			return modelID
		}
	}

	return ""
}

func (oc *AIClient) resolveModelForCapability(
	ctx context.Context,
	meta *PortalMetadata,
	supportsCaps modelCapsFilter,
	fallback func(context.Context, *PortalMetadata) string,
) (string, bool) {
	modelID := oc.effectiveModel(meta)
	caps := getModelCapabilities(modelID, oc.findModelInfo(modelID))
	if supportsCaps(caps) {
		return modelID, false
	}

	if !oc.canUseMediaUnderstanding(meta) {
		return "", false
	}

	fallbackID := fallback(ctx, meta)
	if fallbackID == "" {
		return "", false
	}
	return fallbackID, true
}

// resolveImageUnderstandingModel returns a vision-capable model from the agent's model chain.
func (oc *AIClient) resolveImageUnderstandingModel(ctx context.Context, meta *PortalMetadata) string {
	return oc.resolveUnderstandingModel(
		ctx,
		meta,
		func(caps ModelCapabilities) bool { return caps.SupportsVision },
		func(info ModelInfo) bool { return info.SupportsVision },
		"image",
	)
}

// resolveVisionModelForImage returns the model to use for image analysis.
// The second return value is true when a fallback model (not the effective model) is used.
func (oc *AIClient) resolveVisionModelForImage(ctx context.Context, meta *PortalMetadata) (string, bool) {
	return oc.resolveModelForCapability(
		ctx,
		meta,
		func(caps ModelCapabilities) bool { return caps.SupportsVision },
		oc.resolveImageUnderstandingModel,
	)
}

// resolveAudioUnderstandingModel returns an audio-capable model from the agent's model chain.
func (oc *AIClient) resolveAudioUnderstandingModel(ctx context.Context, meta *PortalMetadata) string {
	return oc.resolveUnderstandingModel(
		ctx,
		meta,
		func(caps ModelCapabilities) bool { return caps.SupportsAudio },
		func(info ModelInfo) bool { return info.SupportsAudio },
		"audio",
	)
}

func (oc *AIClient) pickModelFromCache(cache *ModelCache, provider string, supports modelInfoFilter) string {
	if cache == nil || len(cache.Models) == 0 {
		return ""
	}
	return pickModelFromList(cache.Models, provider, supports)
}

func pickModelFromList(models []ModelInfo, provider string, supports modelInfoFilter) string {
	for _, info := range models {
		if !supports(info) {
			continue
		}
		if !providerMatches(info, provider) {
			continue
		}
		return info.ID
	}
	return ""
}

func providerMatches(info ModelInfo, provider string) bool {
	switch provider {
	case ProviderOpenRouter, ProviderBeeper, ProviderMagicProxy:
		if info.Provider != "" {
			return info.Provider == "openrouter"
		}
		return strings.HasPrefix(info.ID, "openrouter/")
	case ProviderOpenAI:
		if info.Provider != "" {
			return info.Provider == "openai"
		}
		return strings.HasPrefix(info.ID, "openai/")
	default:
		return true
	}
}

// resolveAudioModelForInput returns the model to use for audio analysis.
// The second return value is true when a fallback model (not the effective model) is used.
func (oc *AIClient) resolveAudioModelForInput(ctx context.Context, meta *PortalMetadata) (string, bool) {
	return oc.resolveModelForCapability(
		ctx,
		meta,
		func(caps ModelCapabilities) bool { return caps.SupportsAudio },
		oc.resolveAudioUnderstandingModel,
	)
}

func (oc *AIClient) analyzeImageWithModel(
	ctx context.Context,
	modelID string,
	imageURL string,
	mimeType string,
	encryptedFile *event.EncryptedFileInfo,
	prompt string,
) (string, error) {
	if strings.TrimSpace(modelID) == "" {
		return "", errors.New("missing model for image analysis")
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = defaultImageUnderstandingPrompt
	}

	modelIDForAPI := oc.modelIDForAPI(modelID)
	imageRef := mediaSourceLabel(imageURL, encryptedFile)
	b64Data, actualMimeType, err := oc.downloadMediaBase64(ctx, imageURL, encryptedFile, 20, mimeType)
	if err != nil {
		return "", fmt.Errorf("failed to download image %s for model %s: %w", imageRef, modelIDForAPI, err)
	}
	actualMimeType = strings.TrimSpace(actualMimeType)
	if actualMimeType == "" {
		actualMimeType = strings.TrimSpace(mimeType)
	}
	if actualMimeType == "" {
		actualMimeType = "image/jpeg"
	}

	dataURL := buildDataURL(actualMimeType, b64Data)

	messages := []UnifiedMessage{
		{
			Role: RoleUser,
			Content: []ContentPart{
				{
					Type:     ContentTypeImage,
					ImageURL: dataURL,
					MimeType: actualMimeType,
				},
				{
					Type: ContentTypeText,
					Text: prompt,
				},
			},
		},
	}

	resp, err := oc.provider.Generate(ctx, GenerateParams{
		Model:               modelIDForAPI,
		Messages:            messages,
		MaxCompletionTokens: imageUnderstandingMaxTokens,
	})
	if err != nil {
		return "", fmt.Errorf("image analysis failed for model %s (image %s): %w", modelIDForAPI, imageRef, err)
	}

	return strings.TrimSpace(resp.Content), nil
}

func (oc *AIClient) analyzeAudioWithModel(
	ctx context.Context,
	modelID string,
	audioURL string,
	mimeType string,
	encryptedFile *event.EncryptedFileInfo,
	prompt string,
) (string, error) {
	if strings.TrimSpace(modelID) == "" {
		return "", errors.New("missing model for audio analysis")
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = defaultAudioUnderstandingPrompt
	}

	modelIDForAPI := oc.modelIDForAPI(modelID)
	audioRef := mediaSourceLabel(audioURL, encryptedFile)
	b64Data, actualMimeType, err := oc.downloadMediaBase64(ctx, audioURL, encryptedFile, 25, mimeType)
	if err != nil {
		return "", fmt.Errorf("failed to download audio %s for model %s: %w", audioRef, modelIDForAPI, err)
	}
	actualMimeType = strings.TrimSpace(actualMimeType)
	if actualMimeType == "" {
		actualMimeType = strings.TrimSpace(mimeType)
	}
	format := getAudioFormat(actualMimeType)
	if format == "" {
		format = "mp3"
	}

	messages := []UnifiedMessage{
		{
			Role: RoleUser,
			Content: []ContentPart{
				{
					Type:        ContentTypeAudio,
					AudioB64:    b64Data,
					AudioFormat: format,
				},
				{
					Type: ContentTypeText,
					Text: prompt,
				},
			},
		},
	}

	resp, err := oc.provider.Generate(ctx, GenerateParams{
		Model:               modelIDForAPI,
		Messages:            messages,
		MaxCompletionTokens: imageUnderstandingMaxTokens,
	})
	if err != nil {
		return "", fmt.Errorf("audio analysis failed for model %s (audio %s): %w", modelIDForAPI, audioRef, err)
	}

	return strings.TrimSpace(resp.Content), nil
}

func mediaSourceLabel(mediaURL string, encryptedFile *event.EncryptedFileInfo) string {
	source := strings.TrimSpace(mediaURL)
	if encryptedFile != nil && encryptedFile.URL != "" {
		encryptedURL := strings.TrimSpace(string(encryptedFile.URL))
		if source == "" {
			return encryptedURL
		}
		if encryptedURL != "" && encryptedURL != source {
			return fmt.Sprintf("%s (encrypted %s)", source, encryptedURL)
		}
	}
	if source == "" {
		return "unknown media"
	}
	return source
}

func buildImageUnderstandingPrompt(caption string, hasUserCaption bool) string {
	if hasUserCaption {
		caption = strings.TrimSpace(caption)
		if caption != "" {
			return caption
		}
	}
	return defaultImageUnderstandingPrompt
}

func buildAudioUnderstandingPrompt(caption string, hasUserCaption bool) string {
	if hasUserCaption {
		caption = strings.TrimSpace(caption)
		if caption != "" {
			return caption
		}
	}
	return defaultAudioUnderstandingPrompt
}

func buildImageUnderstandingMessage(caption string, hasUserCaption bool, description string) string {
	description = strings.TrimSpace(description)
	if description == "" {
		return ""
	}

	if !hasUserCaption {
		caption = ""
	}
	caption = strings.TrimSpace(caption)

	lines := []string{"[Image]"}
	if caption != "" {
		lines = append(lines, "User text:\n"+caption)
	}
	lines = append(lines, "Description:\n"+description)
	return strings.Join(lines, "\n")
}

func buildAudioUnderstandingMessage(caption string, hasUserCaption bool, transcript string) string {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return ""
	}

	if !hasUserCaption {
		caption = ""
	}
	caption = strings.TrimSpace(caption)

	lines := []string{"[Audio]"}
	if caption != "" {
		lines = append(lines, "User text:\n"+caption)
	}
	lines = append(lines, "Transcript:\n"+transcript)
	return strings.Join(lines, "\n")
}
