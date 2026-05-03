package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/event"
)

func (oc *AIClient) canUseMediaUnderstanding(meta *PortalMetadata) bool {
	if oc == nil || oc.connector == nil {
		return false
	}
	modelID := oc.effectiveModel(meta)
	if modelID == "" {
		return false
	}
	info := oc.findModelInfo(modelID)
	return info != nil && info.SupportsToolCalling
}

type modelCapsFilter func(ModelCapabilities) bool
type modelInfoFilter func(ModelInfo) bool

func (oc *AIClient) resolveUnderstandingModel(
	ctx context.Context,
	supportsInfo modelInfoFilter,
) string {
	loginState := oc.loginStateSnapshot(ctx)
	provider := loginMetadata(oc.UserLogin).Provider

	// Prefer cached/provider-listed models first.
	if modelID := oc.pickModelFromCache(loginState.ModelCache, provider, supportsInfo); modelID != "" {
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
	responder := oc.responderForMeta(ctx, meta)
	modelID := ""
	caps := ModelCapabilities{}
	if responder != nil {
		modelID = responder.ModelID
		caps = responder.ModelCapabilities()
	}
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

// resolveImageUnderstandingModel returns a vision-capable model.
func (oc *AIClient) resolveImageUnderstandingModel(ctx context.Context, meta *PortalMetadata) string {
	return oc.resolveUnderstandingModel(
		ctx,
		func(info ModelInfo) bool { return info.SupportsVision },
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
	case ProviderOpenRouter, ProviderMagicProxy:
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
		prompt = defaultPromptByCapability[MediaCapabilityImage]
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

	dataURL := BuildDataURL(actualMimeType, b64Data)

	ctxPrompt := UserPromptContext(
		PromptBlock{
			Type:     PromptBlockImage,
			ImageURL: dataURL,
			MimeType: actualMimeType,
		},
		PromptBlock{
			Type: PromptBlockText,
			Text: prompt,
		},
	)

	resp, err := oc.provider.Generate(ctx, GenerateParams{
		Model:               modelIDForAPI,
		Context:             ctxPrompt,
		MaxCompletionTokens: defaultImageUnderstandingLimit,
	})
	if err != nil {
		return "", fmt.Errorf("image analysis failed for model %s (image %s): %w", modelIDForAPI, imageRef, err)
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

func buildMediaPromptFromCaption(caption string, hasUserCaption bool, defaultPrompt string) string {
	if hasUserCaption {
		caption = strings.TrimSpace(caption)
		if caption != "" {
			return caption
		}
	}
	return defaultPrompt
}

func buildMediaUnderstandingPrompt(capability MediaUnderstandingCapability) func(string, bool) string {
	return func(caption string, hasUserCaption bool) string {
		return buildMediaPromptFromCaption(caption, hasUserCaption, defaultPromptByCapability[capability])
	}
}

func buildMediaUnderstandingMessage(title, kind string) func(string, bool, string) string {
	return func(caption string, hasUserCaption bool, text string) string {
		if strings.TrimSpace(text) == "" {
			return ""
		}
		userText := ""
		if hasUserCaption {
			userText = strings.TrimSpace(caption)
		}
		return formatMediaSection(title, kind, strings.TrimSpace(text), userText)
	}
}
