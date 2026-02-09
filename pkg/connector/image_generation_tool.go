package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "golang.org/x/image/webp"

	"github.com/beeper/ai-bridge/pkg/shared/media"
)

type imageGenProvider string

const (
	imageGenProviderOpenAI     imageGenProvider = "openai"
	imageGenProviderGemini     imageGenProvider = "gemini"
	imageGenProviderOpenRouter imageGenProvider = "openrouter"

	imageInputMaxSizeMB = 20
)

type imageGenRequest struct {
	Provider     string
	Prompt       string
	Model        string
	Count        int
	Size         string
	Quality      string
	Style        string
	Background   string
	OutputFormat string
	AspectRatio  string
	Resolution   string
	InputImages  []string
}

type openAIImageParams struct {
	Model        string
	Prompt       string
	Count        int
	Size         string
	Quality      string
	Style        string
	Background   string
	OutputFormat string
}

var imageGenHTTPClient = &http.Client{Timeout: 120 * time.Second}

func parseImageGenArgs(args map[string]any) (imageGenRequest, error) {
	prompt, ok := args["prompt"].(string)
	if !ok || strings.TrimSpace(prompt) == "" {
		return imageGenRequest{}, errors.New("missing or invalid 'prompt' argument")
	}

	req := imageGenRequest{
		Prompt: strings.TrimSpace(prompt),
		Count:  1,
	}

	if v, ok := args["provider"].(string); ok {
		req.Provider = strings.TrimSpace(v)
	}
	if v, ok := args["model"].(string); ok {
		req.Model = strings.TrimSpace(v)
	}
	if rawCount, ok := args["count"]; ok {
		if v, ok := rawCount.(float64); ok {
			if v < 1 || math.Mod(v, 1) != 0 {
				return imageGenRequest{}, errors.New("invalid 'count' argument")
			}
			req.Count = int(v)
		} else if v, ok := rawCount.(int); ok {
			if v < 1 {
				return imageGenRequest{}, errors.New("invalid 'count' argument")
			}
			req.Count = v
		} else {
			return imageGenRequest{}, errors.New("invalid 'count' argument")
		}
	}
	if v, ok := args["size"].(string); ok {
		req.Size = strings.TrimSpace(v)
	}
	if v, ok := args["quality"].(string); ok {
		req.Quality = strings.TrimSpace(v)
	}
	if v, ok := args["style"].(string); ok {
		req.Style = strings.TrimSpace(v)
	}
	if v, ok := args["background"].(string); ok {
		req.Background = strings.TrimSpace(v)
	}
	if v, ok := args["output_format"].(string); ok {
		req.OutputFormat = strings.TrimSpace(v)
	} else if v, ok := args["outputFormat"].(string); ok {
		req.OutputFormat = strings.TrimSpace(v)
	}
	if v, ok := args["aspect_ratio"].(string); ok {
		req.AspectRatio = strings.TrimSpace(v)
	} else if v, ok := args["aspectRatio"].(string); ok {
		req.AspectRatio = strings.TrimSpace(v)
	}
	if v, ok := args["resolution"].(string); ok {
		req.Resolution = strings.TrimSpace(v)
	}

	req.InputImages = append(req.InputImages, readStringSlice(args, "input_images")...)
	req.InputImages = append(req.InputImages, readStringSlice(args, "inputImages")...)
	req.InputImages = append(req.InputImages, readStringSlice(args, "input_image")...)
	req.InputImages = dedupeStrings(req.InputImages)

	return req, nil
}

func readStringSlice(args map[string]any, key string) []string {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil
	}
	if list, ok := raw.([]string); ok {
		return list
	}
	if list, ok := raw.([]any); ok {
		out := make([]string, 0, len(list))
		for _, item := range list {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	}
	if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
		return []string{strings.TrimSpace(s)}
	}
	return nil
}

func dedupeStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func resolveImageGenProvider(req imageGenRequest, btc *BridgeToolContext) (imageGenProvider, error) {
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if provider != "" {
		switch provider {
		case "openai":
			if !supportsOpenAIImageGen(btc) {
				return "", errors.New("openai image generation is not available for this login")
			}
			return imageGenProviderOpenAI, nil
		case "gemini", "google":
			if !supportsGeminiImageGen(btc) {
				return "", errors.New("gemini image generation is not available for this login")
			}
			return imageGenProviderGemini, nil
		case "openrouter":
			if !supportsOpenRouterImageGen(btc) {
				return "", errors.New("openrouter image generation is not available for this login")
			}
			return imageGenProviderOpenRouter, nil
			default:
				return "", fmt.Errorf("unknown image generation provider: %s", provider)
			}
	}

	// Prefer OpenRouter image gen whenever it's available (Gemini models support extra controls).
	if supportsOpenRouterImageGen(btc) {
		return imageGenProviderOpenRouter, nil
	}

	loginMeta := loginMetadata(btc.Client.UserLogin)
	switch loginMeta.Provider {
	case ProviderOpenAI:
		if !supportsOpenAIImageGen(btc) {
			return "", errors.New("openai image generation is not available for this login")
		}
		return imageGenProviderOpenAI, nil
	case ProviderOpenRouter:
		if !supportsOpenRouterImageGen(btc) {
			return "", errors.New("openrouter image generation is not available for this login")
		}
		return imageGenProviderOpenRouter, nil
	case ProviderMagicProxy:
		if supportsOpenRouterImageGen(btc) {
			return imageGenProviderOpenRouter, nil
		}
		if supportsOpenAIImageGen(btc) {
			return imageGenProviderOpenAI, nil
		}
		return "", errors.New("image generation is not available for this login")
	case ProviderBeeper:
		// Beeper: prefer OpenRouter when the request is simple; otherwise use direct adapters.
		openAISupported := supportsOpenAIImageGen(btc)
		geminiSupported := supportsGeminiImageGen(btc)
		openRouterSupported := supportsOpenRouterImageGen(btc)

		if usesGeminiParams(req) {
			if !geminiSupported {
				return "", errors.New("gemini image generation is not available for this login")
			}
			return imageGenProviderGemini, nil
		}
		if usesOpenAIParams(req) {
			if !openAISupported {
				return "", errors.New("openai image generation is not available for this login")
			}
			return imageGenProviderOpenAI, nil
		}
		switch inferProviderFromModel(req.Model) {
		case imageGenProviderOpenAI:
			if openAISupported {
				return imageGenProviderOpenAI, nil
			}
			if openRouterSupported {
				return imageGenProviderOpenRouter, nil
			}
			if geminiSupported {
				return imageGenProviderGemini, nil
			}
			return "", errors.New("openai image generation is not available for this login")
		case imageGenProviderGemini:
			// If no gemini-specific params, OpenRouter is sufficient.
			if openRouterSupported && req.Count <= 1 {
				return imageGenProviderOpenRouter, nil
			}
			if geminiSupported {
				return imageGenProviderGemini, nil
			}
			if openAISupported {
				return imageGenProviderOpenAI, nil
			}
			return "", errors.New("gemini image generation is not available for this login")
		case imageGenProviderOpenRouter:
			if openRouterSupported {
				return imageGenProviderOpenRouter, nil
			}
			if openAISupported {
				return imageGenProviderOpenAI, nil
			}
			if geminiSupported {
				return imageGenProviderGemini, nil
			}
			return "", errors.New("openrouter image generation is not available for this login")
		}
		if openRouterSupported {
			return imageGenProviderOpenRouter, nil
		}
		if openAISupported {
			return imageGenProviderOpenAI, nil
		}
		if geminiSupported {
			return imageGenProviderGemini, nil
		}
		return "", errors.New("image generation is not available for this login")
	default:
		return "", errors.New("unsupported provider for image generation")
	}
}

func inferProviderFromModel(model string) imageGenProvider {
	lower := strings.ToLower(strings.TrimSpace(model))
	if lower == "" {
		return ""
	}
	if strings.HasPrefix(lower, "openai/") || strings.Contains(lower, "dall-e") || strings.Contains(lower, "gpt-image") {
		return imageGenProviderOpenAI
	}
	if strings.HasPrefix(lower, "google/") || strings.Contains(lower, "gemini") {
		return imageGenProviderGemini
	}
	if strings.HasPrefix(lower, "openrouter/") {
		return imageGenProviderOpenRouter
	}
	return ""
}

func usesOpenAIParams(req imageGenRequest) bool {
	return req.Size != "" || req.Quality != "" || req.Style != "" || req.Background != "" || req.OutputFormat != "" || req.Count > 1
}

func usesGeminiParams(req imageGenRequest) bool {
	return req.AspectRatio != "" || req.Resolution != "" || len(req.InputImages) > 0
}

func supportsOpenAIImageGen(btc *BridgeToolContext) bool {
	if btc == nil || btc.Client == nil || btc.Client.UserLogin == nil || btc.Client.UserLogin.Metadata == nil {
		return false
	}
	loginMeta := loginMetadata(btc.Client.UserLogin)
	switch loginMeta.Provider {
	case ProviderOpenAI, ProviderBeeper, ProviderMagicProxy:
		if loginMeta.Provider == ProviderBeeper {
			return btc.Client.connector.resolveBeeperToken(loginMeta) != ""
		}
		if loginMeta.Provider == ProviderMagicProxy {
			// Magic Proxy uses a per-login token+base URL, not the OpenAI config key.
			return strings.TrimSpace(loginMeta.APIKey) != "" && strings.TrimSpace(loginMeta.BaseURL) != ""
		}
		return btc.Client.connector.resolveOpenAIAPIKey(loginMeta) != ""
	default:
		return false
	}
}

func supportsOpenRouterImageGen(btc *BridgeToolContext) bool {
	_, _, ok := resolveOpenRouterImageGenEndpoint(btc)
	return ok
}

func supportsGeminiImageGen(btc *BridgeToolContext) bool {
	if btc == nil || btc.Client == nil || btc.Client.UserLogin == nil || btc.Client.UserLogin.Metadata == nil {
		return false
	}
	loginMeta := loginMetadata(btc.Client.UserLogin)
	if loginMeta.Provider == ProviderBeeper {
		return btc.Client.connector.resolveBeeperToken(loginMeta) != ""
	}
	return false
}

func normalizeOpenAIModel(model string) string {
	if strings.TrimSpace(model) == "" {
		return DefaultOpenAIImageModel
	}
	_, actual := ParseModelPrefix(model)
	actual = strings.TrimSpace(actual)
	actual = strings.TrimPrefix(actual, "openai/")
	if actual == "" {
		return strings.TrimSpace(model)
	}
	return actual
}

func normalizeGeminiModel(model string) string {
	if strings.TrimSpace(model) == "" {
		return DefaultGeminiImageModel
	}
	_, actual := ParseModelPrefix(model)
	actual = strings.TrimSpace(actual)
	actual = strings.TrimPrefix(actual, "google/")
	if actual == "" {
		return strings.TrimSpace(model)
	}
	return actual
}

func normalizeOpenRouterModel(model string) string {
	if strings.TrimSpace(model) == "" {
		return DefaultImageModel
	}
	return strings.TrimSpace(model)
}

func openAIImageFamily(model string) string {
	lower := strings.ToLower(model)
	if strings.Contains(lower, "dall-e-3") || strings.Contains(lower, "dalle-3") {
		return "dall-e-3"
	}
	if strings.Contains(lower, "dall-e-2") || strings.Contains(lower, "dalle-2") {
		return "dall-e-2"
	}
	if strings.Contains(lower, "gpt-image") {
		return "gpt-image"
	}
	return "gpt-image"
}

func normalizeOpenAIImageParams(req imageGenRequest) (openAIImageParams, error) {
	model := normalizeOpenAIModel(req.Model)
	family := openAIImageFamily(model)
	count := req.Count
	if count == 0 {
		count = 1
	}
	if count < 1 {
		return openAIImageParams{}, errors.New("count must be >= 1")
	}
	if count > 10 {
		return openAIImageParams{}, errors.New("count exceeds maximum (10)")
	}

	size := strings.TrimSpace(req.Size)
	quality := strings.ToLower(strings.TrimSpace(req.Quality))
	style := strings.ToLower(strings.TrimSpace(req.Style))
	background := strings.ToLower(strings.TrimSpace(req.Background))
	outputFormat := strings.ToLower(strings.TrimSpace(req.OutputFormat))

	switch family {
	case "dall-e-3":
		if size == "" {
			size = "1024x1024"
		}
		if quality == "" {
			quality = "standard"
		}
		if style == "" {
			style = "vivid"
		}
		if background != "" || outputFormat != "" {
			return openAIImageParams{}, fmt.Errorf("background/output_format are not supported for %s", model)
		}
		if !isAllowedValue(size, allowedDalle3Sizes) {
			return openAIImageParams{}, fmt.Errorf("unsupported size for %s: %s", model, size)
		}
		if !isAllowedValue(quality, allowedDalle3Qualities) {
			return openAIImageParams{}, fmt.Errorf("unsupported quality for %s: %s", model, quality)
		}
		if !isAllowedValue(style, allowedDalle3Styles) {
			return openAIImageParams{}, fmt.Errorf("unsupported style for %s: %s", model, style)
		}
		if count > 1 {
			return openAIImageParams{}, fmt.Errorf("count must be 1 for %s", model)
		}
	case "dall-e-2":
		if size == "" {
			size = "1024x1024"
		}
		if quality == "" {
			quality = "standard"
		}
		if style != "" {
			return openAIImageParams{}, fmt.Errorf("style is not supported for %s", model)
		}
		if background != "" || outputFormat != "" {
			return openAIImageParams{}, fmt.Errorf("background/output_format are not supported for %s", model)
		}
		if !isAllowedValue(size, allowedDalle2Sizes) {
			return openAIImageParams{}, fmt.Errorf("unsupported size for %s: %s", model, size)
		}
		if quality != "standard" {
			return openAIImageParams{}, fmt.Errorf("unsupported quality for %s: %s", model, quality)
		}
	default:
		if size == "" {
			size = "1024x1024"
		}
		if quality == "" {
			quality = "high"
		}
		if background == "" {
			background = "auto"
		}
		if outputFormat == "" {
			outputFormat = "png"
		}
		if style != "" {
			return openAIImageParams{}, fmt.Errorf("style is not supported for %s", model)
		}
		if !isAllowedValue(size, allowedGPTImageSizes) {
			return openAIImageParams{}, fmt.Errorf("unsupported size for %s: %s", model, size)
		}
		if !isAllowedValue(quality, allowedGPTImageQualities) {
			return openAIImageParams{}, fmt.Errorf("unsupported quality for %s: %s", model, quality)
		}
		if background != "" && !isAllowedValue(background, allowedGPTImageBackgrounds) {
			return openAIImageParams{}, fmt.Errorf("unsupported background for %s: %s", model, background)
		}
		if outputFormat != "" && !isAllowedValue(outputFormat, allowedGPTImageFormats) {
			return openAIImageParams{}, fmt.Errorf("unsupported output_format for %s: %s", model, outputFormat)
		}
	}

	return openAIImageParams{
		Model:        model,
		Prompt:       req.Prompt,
		Count:        count,
		Size:         size,
		Quality:      quality,
		Style:        style,
		Background:   background,
		OutputFormat: outputFormat,
	}, nil
}

var (
	allowedDalle3Sizes         = map[string]bool{"1024x1024": true, "1792x1024": true, "1024x1792": true}
	allowedDalle3Qualities     = map[string]bool{"standard": true, "hd": true}
	allowedDalle3Styles        = map[string]bool{"vivid": true, "natural": true}
	allowedDalle2Sizes         = map[string]bool{"256x256": true, "512x512": true, "1024x1024": true}
	allowedGPTImageSizes       = map[string]bool{"1024x1024": true, "1536x1024": true, "1024x1536": true, "auto": true}
	allowedGPTImageQualities   = map[string]bool{"auto": true, "high": true, "medium": true, "low": true}
	allowedGPTImageBackgrounds = map[string]bool{"auto": true, "transparent": true, "opaque": true}
	allowedGPTImageFormats     = map[string]bool{"png": true, "jpeg": true, "webp": true}
	allowedGeminiResolutions   = map[string]bool{"1k": true, "2k": true, "4k": true}
)

func isAllowedValue(value string, allowed map[string]bool) bool {
	if value == "" {
		return true
	}
	return allowed[strings.ToLower(value)]
}

func buildOpenAIImagesBaseURL(btc *BridgeToolContext) (string, error) {
	loginMeta := loginMetadata(btc.Client.UserLogin)
	switch loginMeta.Provider {
	case ProviderBeeper:
		base := strings.TrimSuffix(strings.TrimSpace(btc.Client.connector.resolveBeeperBaseURL(loginMeta)), "/")
		if base == "" {
			return "", errors.New("beeper base_url is required for image generation")
		}
		return base + "/openai/v1", nil
	case ProviderOpenAI:
		base := btc.Client.connector.resolveOpenAIBaseURL()
		return strings.TrimSuffix(base, "/"), nil
	case ProviderMagicProxy:
		if btc.Client.connector != nil {
			services := btc.Client.connector.resolveServiceConfig(loginMeta)
			if svc, ok := services[serviceOpenAI]; ok && strings.TrimSpace(svc.BaseURL) != "" {
				return strings.TrimSuffix(strings.TrimSpace(svc.BaseURL), "/"), nil
			}
		}
		base := normalizeMagicProxyBaseURL(loginMeta.BaseURL)
		if base == "" {
			return "", errors.New("magic proxy base_url is required for image generation")
		}
		return joinProxyPath(base, "/openai/v1"), nil
	default:
		return "", errors.New("openai image generation not available for this provider")
	}
}

func buildGeminiBaseURL(btc *BridgeToolContext) (string, error) {
	loginMeta := loginMetadata(btc.Client.UserLogin)
	switch loginMeta.Provider {
	case ProviderBeeper:
		base := strings.TrimSuffix(strings.TrimSpace(btc.Client.connector.resolveBeeperBaseURL(loginMeta)), "/")
		if base == "" {
			return "", errors.New("beeper base_url is required for gemini image generation")
		}
		return base + "/gemini/v1beta", nil
	default:
		return "", errors.New("gemini image generation not available for this provider")
	}
}

func generateImagesForRequest(ctx context.Context, btc *BridgeToolContext, req imageGenRequest) ([]string, error) {
	provider, err := resolveImageGenProvider(req, btc)
	if err != nil {
		return nil, err
	}

	switch provider {
	case imageGenProviderOpenAI:
		params, err := normalizeOpenAIImageParams(req)
		if err != nil {
			return nil, err
		}
		baseURL, err := buildOpenAIImagesBaseURL(btc)
		if err != nil {
			return nil, err
		}
		return callOpenAIImageGen(ctx, btc.Client.apiKey, baseURL, params)
	case imageGenProviderGemini:
		if req.Count > 1 {
			return nil, errors.New("gemini image generation currently supports count=1")
		}
		model := normalizeGeminiModel(req.Model)
		baseURL, err := buildGeminiBaseURL(btc)
		if err != nil {
			return nil, err
		}
		return callGeminiImageGen(ctx, btc, baseURL, model, req)
	case imageGenProviderOpenRouter:
		// We'll emulate count>1 by making multiple calls.
		// Ignore OpenAI-specific controls (common with agent tools) rather than failing the request.
		req.Size = ""
		req.Quality = ""
		req.Style = ""
		req.Background = ""
		req.OutputFormat = ""
		model := normalizeOpenRouterModel(req.Model)
		// If the request looks like it's targeting an OpenAI image model, force the OpenRouter
		// default (Gemini) instead.
		if inferProviderFromModel(model) == imageGenProviderOpenAI {
			model = DefaultImageModel
		}
		openRouterBaseURL, openRouterAPIKey, ok := resolveOpenRouterImageGenEndpoint(btc)
		if !ok {
			return nil, errors.New("openrouter image generation is not available for this login")
		}
		count := req.Count
		if count < 1 {
			count = 1
		}
		// Parallelize multi-image generation to reduce wall time.
		type genResult struct {
			images []string
			err    error
		}
		concurrency := 3
		if count < concurrency {
			concurrency = count
		}
		sem := make(chan struct{}, concurrency)
		results := make(chan genResult, count)
			for i := 0; i < count; i++ {
				sem <- struct{}{}
				go func() {
					defer func() { <-sem }()
					out, err := callOpenRouterImageGenWithControls(ctx, btc, openRouterAPIKey, openRouterBaseURL, req, model)
					results <- genResult{images: out, err: err}
				}()
			}
			images := make([]string, 0, count)
			for i := 0; i < count; i++ {
			r := <-results
			if r.err != nil {
				return nil, r.err
			}
			images = append(images, r.images...)
		}
		if len(images) > count {
			images = images[:count]
		}
		return images, nil
	default:
		return nil, errors.New("unsupported image generation provider")
	}
}

// resolveOpenRouterImageGenEndpoint returns the OpenRouter base URL + API key for image generation.
// This is used even when the "primary" provider is not OpenRouter (e.g. Magic Proxy, OpenAI) as
// long as an OpenRouter token+endpoint are configured.
func resolveOpenRouterImageGenEndpoint(btc *BridgeToolContext) (baseURL string, apiKey string, ok bool) {
	if btc == nil || btc.Client == nil || btc.Client.UserLogin == nil || btc.Client.UserLogin.Metadata == nil {
		return "", "", false
	}
	meta := loginMetadata(btc.Client.UserLogin)
	conn := btc.Client.connector

	trim := func(s string) string { return strings.TrimSpace(s) }

	// Provider-specific per-login endpoints.
	switch meta.Provider {
	case ProviderMagicProxy:
		base := normalizeMagicProxyBaseURL(meta.BaseURL)
		key := trim(meta.APIKey)
		if base == "" || key == "" {
			return "", "", false
		}
		return joinProxyPath(base, "/openrouter/v1"), key, true
	case ProviderBeeper:
		if conn == nil {
			return "", "", false
		}
		base := strings.TrimSuffix(trim(btc.Client.connector.resolveBeeperBaseURL(meta)), "/")
		key := trim(btc.Client.connector.resolveBeeperToken(meta))
		if base == "" || key == "" {
			return "", "", false
		}
		return base + "/openrouter/v1", key, true
	case ProviderOpenRouter:
		if conn == nil {
			return "", "", false
		}
		base := trim(conn.resolveOpenRouterBaseURL())
		key := trim(conn.resolveOpenRouterAPIKey(meta))
		if base == "" || key == "" {
			return "", "", false
		}
		return strings.TrimSuffix(base, "/"), key, true
	}

	// Global OpenRouter config (available regardless of primary provider).
	if conn == nil {
		return "", "", false
	}
	base := trim(conn.resolveOpenRouterBaseURL())
	key := trim(conn.resolveOpenRouterAPIKey(meta))
	if base == "" || key == "" {
		return "", "", false
	}
	return strings.TrimSuffix(base, "/"), key, true
}

func openRouterImageURLForRef(ctx context.Context, btc *BridgeToolContext, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("empty image reference")
	}

	if strings.HasPrefix(ref, "data:") {
		return ref, nil
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		if err := validateExternalImageURL(ctx, ref); err != nil {
			return "", err
		}
		return ref, nil
	}
	if strings.HasPrefix(ref, "mxc://") {
		b64Data, mimeType, err := btc.Client.downloadAndEncodeMedia(ctx, ref, nil, imageInputMaxSizeMB)
		if err != nil {
			return "", err
		}
		return "data:" + mimeType + ";base64," + b64Data, nil
	}
	if isLocalImageRef(ref) {
		resolved, err := resolveLocalImagePath(ref)
		if err != nil {
			return "", err
		}
		b64Data, mimeType, err := btc.Client.downloadAndEncodeMedia(ctx, resolved, nil, imageInputMaxSizeMB)
		if err != nil {
			return "", err
		}
		return "data:" + mimeType + ";base64," + b64Data, nil
	}

	return "", fmt.Errorf("unsupported image reference: %s", ref)
}

func callOpenRouterImageGenWithControls(ctx context.Context, btc *BridgeToolContext, apiKey, baseURL string, req imageGenRequest, model string) ([]string, error) {
	// OpenRouter image generation uses /chat/completions with modalities=["image","text"].
	msg := map[string]any{
		"role": "user",
	}

	if len(req.InputImages) > 0 {
		parts := make([]map[string]any, 0, len(req.InputImages)+1)
		for _, ref := range req.InputImages {
			url, err := openRouterImageURLForRef(ctx, btc, ref)
			if err != nil {
				return nil, err
			}
			parts = append(parts, map[string]any{
				"type": "image_url",
				"image_url": map[string]any{
					"url": url,
				},
			})
		}
		parts = append(parts, map[string]any{
			"type": "text",
			"text": req.Prompt,
		})
		msg["content"] = parts
	} else {
		msg["content"] = req.Prompt
	}

	reqBody := map[string]any{
		"model":      model,
		"messages":   []map[string]any{msg},
		"modalities": []string{"image", "text"},
		"stream":     false,
	}

	imageCfg := map[string]any{}
	if ar := strings.TrimSpace(req.AspectRatio); ar != "" {
		imageCfg["aspect_ratio"] = ar
	}
	if res := strings.ToUpper(strings.TrimSpace(req.Resolution)); res != "" {
		imageCfg["image_size"] = res
	}
	if len(imageCfg) > 0 {
		reqBody["image_config"] = imageCfg
	}

	return callOpenRouterImageGen(ctx, apiKey, baseURL, reqBody)
}

func callOpenAIImageGen(ctx context.Context, apiKey, baseURL string, params openAIImageParams) ([]string, error) {
	reqBody := map[string]any{
		"model":  params.Model,
		"prompt": params.Prompt,
		"n":      params.Count,
		"size":   params.Size,
	}

	family := openAIImageFamily(params.Model)
	if family != "dall-e-2" {
		reqBody["quality"] = params.Quality
	}
	if params.Style != "" {
		reqBody["style"] = params.Style
	}
	if params.Background != "" {
		reqBody["background"] = params.Background
	}
	if params.OutputFormat != "" {
		reqBody["output_format"] = params.OutputFormat
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/images/generations", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := imageGenHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			URL     string `json:"url"`
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.Data) == 0 {
		return nil, errors.New("no image data in response")
	}

	images := make([]string, 0, len(result.Data))
	for _, item := range result.Data {
		if item.B64JSON != "" {
			images = append(images, item.B64JSON)
			continue
		}
		if item.URL != "" {
			imgB64, err := fetchImageAsBase64(ctx, item.URL)
			if err != nil {
				return nil, err
			}
			images = append(images, imgB64)
		}
	}

	if len(images) == 0 {
		return nil, errors.New("no image data in response")
	}
	return images, nil
}

func callGeminiImageGen(ctx context.Context, btc *BridgeToolContext, baseURL, model string, req imageGenRequest) ([]string, error) {
	inputs, maxDim, err := loadGeminiInputs(ctx, btc, req.InputImages)
	if err != nil {
		return nil, err
	}

	resolution := strings.ToUpper(strings.TrimSpace(req.Resolution))
	if resolution == "" {
		resolution = "1K"
		if maxDim >= 3000 {
			resolution = "4K"
		} else if maxDim >= 1500 {
			resolution = "2K"
		}
	}
	if !isAllowedValue(strings.ToLower(resolution), allowedGeminiResolutions) {
		return nil, fmt.Errorf("unsupported resolution: %s", resolution)
	}

	parts := make([]map[string]any, 0, len(inputs)+1)
	for _, input := range inputs {
		parts = append(parts, map[string]any{
			"inlineData": map[string]any{
				"mimeType": input.MimeType,
				"data":     input.Base64,
			},
		})
	}
	parts = append(parts, map[string]any{
		"text": req.Prompt,
	})

	reqBody := map[string]any{
		"contents": []map[string]any{
			{
				"role":  "user",
				"parts": parts,
			},
		},
		"generationConfig": map[string]any{
			"responseModalities": []string{"TEXT", "IMAGE"},
			"imageConfig": map[string]any{
				"imageSize": resolution,
			},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint := strings.TrimSuffix(baseURL, "/") + "/models/" + model + ":generateContent"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+btc.Client.apiKey)

	resp, err := imageGenHTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text       string `json:"text"`
					InlineData struct {
						MimeType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData"`
					InlineDataAlt struct {
						MimeType string `json:"mime_type"`
						Data     string `json:"data"`
					} `json:"inline_data"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var images []string
	for _, cand := range result.Candidates {
		for _, part := range cand.Content.Parts {
			if part.InlineData.Data != "" {
				images = append(images, part.InlineData.Data)
			} else if part.InlineDataAlt.Data != "" {
				images = append(images, part.InlineDataAlt.Data)
			}
		}
	}

	if len(images) == 0 {
		return nil, errors.New("no image data in response")
	}

	return images, nil
}

type geminiInput struct {
	Base64   string
	MimeType string
}

func loadGeminiInputs(ctx context.Context, btc *BridgeToolContext, refs []string) ([]geminiInput, int, error) {
	if len(refs) == 0 {
		return nil, 0, nil
	}
	if len(refs) > 14 {
		return nil, 0, fmt.Errorf("too many input images (%d), maximum is 14", len(refs))
	}

	inputs := make([]geminiInput, 0, len(refs))
	maxDim := 0
	for _, ref := range refs {
		b64Data, mimeType, err := loadInputImageBase64(ctx, btc, ref)
		if err != nil {
			return nil, 0, err
		}
		if !strings.HasPrefix(mimeType, "image/") {
			return nil, 0, fmt.Errorf("unsupported image type: %s", mimeType)
		}

		inputs = append(inputs, geminiInput{Base64: b64Data, MimeType: mimeType})

		if data, _, err := media.DecodeBase64(b64Data); err == nil {
			if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
				if cfg.Width > maxDim {
					maxDim = cfg.Width
				}
				if cfg.Height > maxDim {
					maxDim = cfg.Height
				}
			}
		}
	}

	return inputs, maxDim, nil
}

func loadInputImageBase64(ctx context.Context, btc *BridgeToolContext, ref string) (string, string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", "", errors.New("empty image reference")
	}

	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "data:") {
		b64Data, mimeType, err := media.ParseDataURI(ref)
		if err != nil {
			return "", "", err
		}
		return b64Data, mimeType, nil
	}

	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		if err := validateExternalImageURL(ctx, ref); err != nil {
			return "", "", err
		}
		b64Data, headerMime, err := fetchImageAsBase64WithType(ctx, ref)
		if err != nil {
			return "", "", err
		}
		mimeType := normalizeMimeString(headerMime)
		if mimeType == "" || !strings.HasPrefix(mimeType, "image/") {
			_, detectedMime, err := media.DecodeBase64(b64Data)
			if err != nil {
				return "", "", err
			}
			mimeType = detectedMime
		}
		return b64Data, mimeType, nil
	}

	if strings.HasPrefix(ref, "mxc://") {
		b64Data, mimeType, err := btc.Client.downloadAndEncodeMedia(ctx, ref, nil, imageInputMaxSizeMB)
		if err != nil {
			return "", "", err
		}
		return b64Data, mimeType, nil
	}

	if isLocalImageRef(ref) {
		resolved, err := resolveLocalImagePath(ref)
		if err != nil {
			return "", "", err
		}
		b64Data, mimeType, err := btc.Client.downloadAndEncodeMedia(ctx, resolved, nil, imageInputMaxSizeMB)
		if err != nil {
			return "", "", err
		}
		return b64Data, mimeType, nil
	}

	return "", "", fmt.Errorf("unsupported image reference: %s", ref)
}

var imageFetchBlockedCIDRs = []*net.IPNet{
	mustParseCIDR("127.0.0.0/8"),
	mustParseCIDR("10.0.0.0/8"),
	mustParseCIDR("172.16.0.0/12"),
	mustParseCIDR("192.168.0.0/16"),
	mustParseCIDR("169.254.0.0/16"),
	mustParseCIDR("::1/128"),
}

var imageFetchMetadataIP = net.ParseIP("169.254.169.254")

func validateExternalImageURL(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid image URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported image URL scheme: %s", parsed.Scheme)
	}

	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return errors.New("image URL missing host")
	}
	if host == "localhost" {
		return errors.New("image URL host is not allowed")
	}

	if ip := net.ParseIP(host); ip != nil {
		if isDisallowedImageIP(ip) {
			return errors.New("image URL host is not allowed")
		}
		return nil
	}

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("failed to resolve image URL host: %w", err)
	}
	if len(ips) == 0 {
		return errors.New("failed to resolve image URL host")
	}
	for _, ip := range ips {
		if isDisallowedImageIP(ip.IP) {
			return errors.New("image URL host is not allowed")
		}
	}

	return nil
}

func isDisallowedImageIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	if ip.IsLoopback() {
		return true
	}
	if ip.Equal(imageFetchMetadataIP) {
		return true
	}
	for _, cidr := range imageFetchBlockedCIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func mustParseCIDR(value string) *net.IPNet {
	_, parsed, err := net.ParseCIDR(value)
	if err != nil {
		panic(fmt.Sprintf("invalid CIDR %q: %v", value, err))
	}
	return parsed
}

func isLocalImageRef(ref string) bool {
	return strings.HasPrefix(ref, "file://") || strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "~") || strings.HasPrefix(ref, ".")
}

func resolveLocalImagePath(ref string) (string, error) {
	pathValue := strings.TrimSpace(ref)
	if strings.HasPrefix(pathValue, "file://") {
		parsedPath, err := fileURLToPath(pathValue)
		if err != nil {
			return "", err
		}
		pathValue = parsedPath
	}

	pathValue = expandUserPath(pathValue)
	cleaned := filepath.Clean(pathValue)
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("failed to resolve local image path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve local image path: %w", err)
	}

	allowedDirs, err := resolvePermittedImageInputDirs()
	if err != nil {
		return "", err
	}
	if !pathWithinAllowedDirs(resolved, allowedDirs) {
		return "", errors.New("local image path is not within allowed directories")
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("failed to stat local image path: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("local image path is not a regular file")
	}
	if info.Mode().Perm()&0o444 == 0 {
		return "", errors.New("local image path is not readable")
	}

	return resolved, nil
}

func fileURLToPath(ref string) (string, error) {
	parsed, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("invalid file URL: %w", err)
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf("invalid file URL scheme: %s", parsed.Scheme)
	}
	if parsed.Host != "" && parsed.Host != "localhost" {
		return "", fmt.Errorf("unsupported file URL host: %s", parsed.Host)
	}
	pathValue := parsed.Path
	if pathValue == "" {
		pathValue = parsed.Opaque
	}
	if unescaped, err := url.PathUnescape(pathValue); err == nil {
		pathValue = unescaped
	}
	if pathValue == "" {
		return "", errors.New("file URL missing path")
	}
	return pathValue, nil
}

func resolvePermittedImageInputDirs() ([]string, error) {
	rawDirs := permittedImageInputDirs()
	if len(rawDirs) == 0 {
		return nil, errors.New("no permitted directories available for local image access")
	}
	dirs := make([]string, 0, len(rawDirs))
	for _, dir := range rawDirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		cleaned := filepath.Clean(dir)
		absDir, err := filepath.Abs(cleaned)
		if err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(absDir)
		if err != nil {
			continue
		}
		dirs = append(dirs, resolved)
	}
	if len(dirs) == 0 {
		return nil, errors.New("no permitted directories available for local image access")
	}
	return dirs, nil
}

func permittedImageInputDirs() []string {
	var dirs []string
	if tempDir := os.TempDir(); strings.TrimSpace(tempDir) != "" {
		dirs = append(dirs, tempDir)
	}
	if homeDir, err := os.UserHomeDir(); err == nil && strings.TrimSpace(homeDir) != "" {
		dirs = append(dirs, homeDir)
	}
	return dirs
}

func pathWithinAllowedDirs(path string, allowedDirs []string) bool {
	for _, dir := range allowedDirs {
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			continue
		}
		if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..") {
			return true
		}
	}
	return false
}
