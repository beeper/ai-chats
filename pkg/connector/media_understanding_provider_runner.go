package connector

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-chats/pkg/shared/stringutil"
)

func (oc *AIClient) describeImageWithEntry(
	ctx context.Context,
	entry MediaUnderstandingModelConfig,
	capCfg *MediaUnderstandingConfig,
	mediaURL string,
	mimeType string,
	encryptedFile *event.EncryptedFileInfo,
	maxBytes int,
	maxChars int,
	prompt string,
	attachmentIndex int,
) (*MediaUnderstandingOutput, error) {
	modelID := strings.TrimSpace(entry.Model)
	if modelID == "" {
		return nil, errors.New("image understanding requires model id")
	}
	entryProvider := normalizeMediaProviderID(entry.Provider)
	if entryProvider != "" {
		currentProvider := normalizeMediaProviderID(loginMetadata(oc.UserLogin).Provider)
		if entryProvider != "" && currentProvider != "" && entryProvider != currentProvider && entryProvider != "openrouter" && entryProvider != "google" {
			return nil, fmt.Errorf("image provider %s not available for current login provider", entryProvider)
		}
	}

	if entryProvider == "google" {
		data, actualMime, err := oc.downloadMediaBytes(ctx, mediaURL, encryptedFile, maxBytes, mimeType)
		if err != nil {
			return nil, err
		}
		if actualMime == "" {
			actualMime = mimeType
		}
		timeout := resolveMediaTimeoutSeconds(entry.TimeoutSeconds, capCfg, defaultTimeoutSecondsByCapability[MediaCapabilityImage])
		return oc.callGeminiMediaCapability(ctx, MediaCapabilityImage, entry, capCfg, data, actualMime, prompt, timeout, maxChars, attachmentIndex)
	}

	rawData, actualMime, err := oc.downloadMediaBytes(ctx, mediaURL, encryptedFile, maxBytes, mimeType)
	if err != nil {
		return nil, err
	}
	if actualMime == "" {
		actualMime = mimeType
	}
	if actualMime == "" {
		actualMime = "image/jpeg"
	}
	b64Data := base64.StdEncoding.EncodeToString(rawData)
	dataURL := BuildDataURL(actualMime, b64Data)

	ctxPrompt := UserPromptContext(
		PromptBlock{
			Type: PromptBlockText,
			Text: prompt,
		},
		PromptBlock{
			Type:     PromptBlockImage,
			ImageURL: dataURL,
			MimeType: actualMime,
		},
	)
	modelIDForAPI := oc.modelIDForAPI(strings.TrimSpace(modelID))
	var resp *GenerateResponse
	if entryProvider == "openrouter" {
		resp, err = oc.generateWithOpenRouter(ctx, modelIDForAPI, ctxPrompt, capCfg, entry)
	} else {
		resp, err = oc.provider.Generate(ctx, GenerateParams{
			Model:               modelIDForAPI,
			Context:             ctxPrompt,
			MaxCompletionTokens: defaultImageUnderstandingLimit,
		})
	}
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(resp.Content)
	text = truncateText(text, maxChars)
	return buildMediaOutput(MediaCapabilityImage, text, entry.Provider, modelID, attachmentIndex), nil
}

func (oc *AIClient) transcribeAudioWithEntry(
	ctx context.Context,
	entry MediaUnderstandingModelConfig,
	capCfg *MediaUnderstandingConfig,
	mediaURL string,
	mimeType string,
	encryptedFile *event.EncryptedFileInfo,
	fileName string,
	maxBytes int,
	maxChars int,
	prompt string,
	timeout time.Duration,
	attachmentIndex int,
) (*MediaUnderstandingOutput, error) {
	providerID := normalizeMediaProviderID(entry.Provider)
	if providerID == "" {
		return nil, errors.New("missing audio provider")
	}
	data, actualMime, err := oc.downloadMediaBytes(ctx, mediaURL, encryptedFile, maxBytes, mimeType)
	if err != nil {
		return nil, err
	}
	if actualMime == "" {
		actualMime = mimeType
	}
	fileName = resolveMediaFileName(fileName, string(MediaCapabilityAudio), mediaURL)

	headers := mergeMediaHeaders(capCfg, entry)
	apiKey := oc.resolveMediaProviderAPIKey(providerID, entry.Profile, entry.PreferredProfile)
	if apiKey == "" && !hasProviderAuthHeader(providerID, headers) {
		return nil, fmt.Errorf("missing API key for %s audio transcription", providerID)
	}

	request := mediaAudioRequest{
		mediaRequestBase: mediaRequestBase{
			APIKey:   apiKey,
			BaseURL:  resolveMediaBaseURL(capCfg, entry),
			Headers:  headers,
			Model:    strings.TrimSpace(entry.Model),
			Prompt:   prompt,
			MimeType: actualMime,
			Data:     data,
			Timeout:  timeout,
		},
		Provider: providerID,
		Language: resolveMediaLanguage(entry, capCfg),
		FileName: fileName,
	}
	if providerID == "openai" && strings.TrimSpace(request.BaseURL) == "" {
		request.BaseURL = resolveOpenAIMediaBaseURL(oc)
	}

	var text string
	switch providerID {
	case "openai", "groq":
		text, err = transcribeOpenAICompatibleAudio(ctx, request)
	case "deepgram":
		query := resolveProviderQuery("deepgram", capCfg, entry)
		text, err = transcribeDeepgramAudio(ctx, request, query)
	case "google":
		text, err = callGeminiForCapability(ctx, request.mediaRequestBase, MediaCapabilityAudio)
	default:
		err = fmt.Errorf("unsupported audio provider: %s", providerID)
	}
	if err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	text = truncateText(text, maxChars)
	return buildMediaOutput(MediaCapabilityAudio, text, providerID, entry.Model, attachmentIndex), nil
}

func (oc *AIClient) describeVideoWithEntry(
	ctx context.Context,
	entry MediaUnderstandingModelConfig,
	capCfg *MediaUnderstandingConfig,
	mediaURL string,
	mimeType string,
	encryptedFile *event.EncryptedFileInfo,
	maxBytes int,
	maxChars int,
	prompt string,
	timeout time.Duration,
	attachmentIndex int,
) (*MediaUnderstandingOutput, error) {
	providerID := normalizeMediaProviderID(entry.Provider)
	if providerID == "" {
		return nil, errors.New("missing video provider")
	}

	// Download and check base64 size limit (shared by all video providers).
	data, actualMime, err := oc.downloadMediaBytes(ctx, mediaURL, encryptedFile, maxBytes, mimeType)
	if err != nil {
		return nil, err
	}
	if actualMime == "" {
		actualMime = mimeType
	}
	if actualMime == "" {
		actualMime = "video/mp4"
	}
	base64Size := estimateBase64Size(len(data))
	maxBase64 := resolveVideoMaxBase64Bytes(maxBytes)
	if base64Size > maxBase64 {
		oc.loggerForContext(ctx).Warn().
			Int("base64_bytes", base64Size).
			Int("limit_bytes", maxBase64).
			Str("provider", providerID).
			Msg("Video payload exceeds base64 limit")
		return nil, errors.New("video payload exceeds base64 limit")
	}

	if providerID != "google" {
		return nil, fmt.Errorf("unsupported video provider: %s", providerID)
	}

	return oc.callGeminiMediaCapability(ctx, MediaCapabilityVideo, entry, capCfg, data, actualMime, prompt, timeout, maxChars, attachmentIndex)
}

func (oc *AIClient) callGeminiMediaCapability(
	ctx context.Context,
	capability MediaUnderstandingCapability,
	entry MediaUnderstandingModelConfig,
	capCfg *MediaUnderstandingConfig,
	data []byte,
	actualMime string,
	prompt string,
	timeout time.Duration,
	maxChars int,
	attachmentIndex int,
) (*MediaUnderstandingOutput, error) {
	headers := mergeMediaHeaders(capCfg, entry)
	apiKey := oc.resolveMediaProviderAPIKey("google", entry.Profile, entry.PreferredProfile)
	if apiKey == "" && !hasProviderAuthHeader("google", headers) {
		return nil, fmt.Errorf("missing API key for google %s", capability)
	}
	request := mediaRequestBase{
		APIKey:   apiKey,
		BaseURL:  resolveMediaBaseURL(capCfg, entry),
		Headers:  headers,
		Model:    strings.TrimSpace(entry.Model),
		Prompt:   prompt,
		MimeType: actualMime,
		Data:     data,
		Timeout:  timeout,
	}
	text, err := callGeminiForCapability(ctx, request, capability)
	if err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	text = truncateText(text, maxChars)
	return buildMediaOutput(capability, text, "google", entry.Model, attachmentIndex), nil
}

func (oc *AIClient) generateWithOpenRouter(
	ctx context.Context,
	modelID string,
	promptContext PromptContext,
	capCfg *MediaUnderstandingConfig,
	entry MediaUnderstandingModelConfig,
) (*GenerateResponse, error) {
	if oc == nil || oc.connector == nil {
		return nil, errors.New("missing connector")
	}
	headers := openRouterHeaders()
	for key, value := range mergeMediaHeaders(capCfg, entry) {
		headers[key] = value
	}
	apiKey := strings.TrimSpace(oc.resolveMediaProviderAPIKey("openrouter", entry.Profile, entry.PreferredProfile))
	if apiKey == "" && !hasProviderAuthHeader("openrouter", headers) {
		return nil, errors.New("missing API key for openrouter")
	}
	baseURL := strings.TrimSpace(resolveMediaBaseURL(capCfg, entry))
	if baseURL == "" {
		baseURL = resolveOpenRouterMediaBaseURL(oc)
	}
	pdfEngine := oc.defaultPDFEngine()
	userID := ""
	if oc.UserLogin != nil && oc.UserLogin.User != nil && oc.UserLogin.User.MXID != "" {
		userID = oc.UserLogin.User.MXID.String()
	}
	provider, err := NewOpenAIProviderWithPDFPlugin(apiKey, baseURL, userID, pdfEngine, headers, oc.log)
	if err != nil {
		return nil, err
	}
	params := GenerateParams{
		Model:               modelID,
		Context:             promptContext,
		MaxCompletionTokens: defaultImageUnderstandingLimit,
	}
	return provider.Generate(ctx, params)
}

func resolveOpenRouterMediaBaseURL(oc *AIClient) string {
	if oc == nil || oc.connector == nil {
		return defaultOpenRouterBaseURL
	}
	loginCfg := oc.loginConfigSnapshot(context.Background())
	if svc := oc.connector.resolveServiceConfig(loginMetadata(oc.UserLogin).Provider, loginCfg)[serviceOpenRouter]; strings.TrimSpace(svc.BaseURL) != "" {
		return strings.TrimRight(svc.BaseURL, "/")
	}
	base := strings.TrimSpace(oc.connector.modelProviderConfig(ProviderOpenRouter).BaseURL)
	if base != "" {
		return strings.TrimRight(base, "/")
	}
	return defaultOpenRouterBaseURL
}

func resolveOpenAIMediaBaseURL(oc *AIClient) string {
	if oc == nil || oc.connector == nil {
		return defaultOpenAITranscriptionBaseURL
	}
	loginCfg := oc.loginConfigSnapshot(context.Background())
	if svc := oc.connector.resolveServiceConfig(loginMetadata(oc.UserLogin).Provider, loginCfg)[serviceOpenAI]; strings.TrimSpace(svc.BaseURL) != "" {
		return stringutil.NormalizeBaseURL(svc.BaseURL)
	}
	if base := stringutil.NormalizeBaseURL(oc.connector.modelProviderConfig(ProviderOpenAI).BaseURL); base != "" {
		return base
	}
	return defaultOpenAITranscriptionBaseURL
}

func resolveMediaBaseURL(cfg *MediaUnderstandingConfig, entry MediaUnderstandingModelConfig) string {
	if strings.TrimSpace(entry.BaseURL) != "" {
		return entry.BaseURL
	}
	if cfg != nil && strings.TrimSpace(cfg.BaseURL) != "" {
		return cfg.BaseURL
	}
	return ""
}

func mergeMediaHeaders(cfg *MediaUnderstandingConfig, entry MediaUnderstandingModelConfig) map[string]string {
	merged := map[string]string{}
	if cfg != nil {
		for key, value := range cfg.Headers {
			merged[key] = value
		}
	}
	for key, value := range entry.Headers {
		merged[key] = value
	}
	return merged
}

func hasProviderAuthHeader(providerID string, headers map[string]string) bool {
	spec, ok := mediaProviderSpecFor(providerID)
	if !ok || spec.authHeader == "" {
		return false
	}
	for key := range headers {
		if strings.EqualFold(key, spec.authHeader) {
			return true
		}
	}
	return false
}

func resolveProfiledEnvKey(base string, profile string) string {
	if base == "" || strings.TrimSpace(profile) == "" {
		return ""
	}
	normalized := strings.TrimSpace(profile)
	normalized = strings.ToUpper(normalized)
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, ".", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	env := base + "_" + normalized
	return strings.TrimSpace(os.Getenv(env))
}

// resolveProfiledKeys tries resolveProfiledEnvKey for each envBase with the given
// profile and preferredProfile, then falls back to the plain env var.
// Returns the first non-empty result.
func resolveProfiledKeys(envBases []string, profile, preferredProfile string) string {
	for _, base := range envBases {
		if key := resolveProfiledEnvKey(base, profile); key != "" {
			return key
		}
		if key := resolveProfiledEnvKey(base, preferredProfile); key != "" {
			return key
		}
	}
	for _, base := range envBases {
		if key := strings.TrimSpace(os.Getenv(base)); key != "" {
			return key
		}
	}
	return ""
}

func (oc *AIClient) resolveMediaProviderAPIKey(providerID string, profile string, preferredProfile string) string {
	spec, ok := mediaProviderSpecFor(providerID)
	if !ok {
		return ""
	}
	if key := resolveProfiledKeys(spec.envKeys, profile, preferredProfile); key != "" {
		return key
	}
	if spec.service != "" {
		if oc == nil || oc.connector == nil || oc.UserLogin == nil || oc.UserLogin.Metadata == nil {
			return ""
		}
		loginCfg := oc.loginConfigSnapshot(context.Background())
		return strings.TrimSpace(oc.connector.resolveServiceConfig(loginMetadata(oc.UserLogin).Provider, loginCfg)[spec.service].APIKey)
	}
	return ""
}
func estimateBase64Size(size int) int {
	if size <= 0 {
		return 0
	}
	return ((size + 2) / 3) * 4
}
