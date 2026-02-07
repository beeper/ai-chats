package connector

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/media"
)

// executeAnalyzeImage analyzes an image with a custom prompt using vision capabilities.
func executeAnalyzeImage(ctx context.Context, args map[string]any) (string, error) {
	imageURL := ""
	if v, ok := args["image"].(string); ok && strings.TrimSpace(v) != "" {
		imageURL = strings.TrimSpace(v)
	} else if v, ok := args["image_url"].(string); ok && strings.TrimSpace(v) != "" {
		imageURL = strings.TrimSpace(v)
	} else if v, ok := args["imageUrl"].(string); ok && strings.TrimSpace(v) != "" {
		imageURL = strings.TrimSpace(v)
	}
	if imageURL == "" {
		return "", errors.New("missing or invalid 'image' argument")
	}

	prompt := ""
	if v, ok := args["prompt"].(string); ok && strings.TrimSpace(v) != "" {
		prompt = strings.TrimSpace(v)
	}
	if prompt == "" {
		prompt = "Describe the image."
	}

	btc := GetBridgeToolContext(ctx)
	if btc == nil {
		return "", errors.New("image requires bridge context")
	}

	if btc.Meta == nil {
		return "", errors.New("missing room metadata for image analysis")
	}
	modelID, _ := btc.Client.resolveVisionModelForImage(ctx, btc.Meta)
	if modelID == "" {
		return "", errors.New("no vision-capable model available for image analysis")
	}

	// Get image data based on URL type
	var imageB64, mimeType string
	var err error

	if strings.HasPrefix(imageURL, "data:") {
		// Parse data URI (data:image/png;base64,...)
		imageB64, mimeType, err = media.ParseDataURI(imageURL)
		if err != nil {
			return "", fmt.Errorf("failed to parse data URI: %w", err)
		}
	} else if strings.HasPrefix(imageURL, "mxc://") ||
		strings.HasPrefix(imageURL, "file://") ||
		strings.HasPrefix(imageURL, "/") ||
		strings.HasPrefix(imageURL, "~") ||
		strings.HasPrefix(imageURL, ".") {
		// Matrix media URL or local file path
		resolved := expandUserPath(imageURL)
		imageB64, mimeType, err = btc.Client.downloadAndEncodeMedia(ctx, resolved, nil, 20)
		if err != nil {
			return "", fmt.Errorf("failed to load image: %w", err)
		}
	} else if strings.HasPrefix(imageURL, "http://") || strings.HasPrefix(imageURL, "https://") {
		// HTTP(S) URL - fetch and encode
		imageB64, err = fetchImageAsBase64(ctx, imageURL)
		if err != nil {
			return "", fmt.Errorf("failed to fetch image: %w", err)
		}
		// Infer mime type from URL or default to jpeg
		mimeType = inferMimeTypeFromURL(imageURL)
	} else {
		return "", errors.New("unsupported URL scheme, must be http://, https://, mxc://, or data URL")
	}

	// Build vision request with image and prompt
	messages := []UnifiedMessage{
		{
			Role: RoleUser,
			Content: []ContentPart{
				{
					Type:     ContentTypeImage,
					ImageB64: imageB64,
					MimeType: mimeType,
				},
				{
					Type: ContentTypeText,
					Text: prompt,
				},
			},
		},
	}

	// Call the AI provider for vision analysis
	resp, err := btc.Client.provider.Generate(ctx, GenerateParams{
		Model:               btc.Client.modelIDForAPI(modelID),
		Messages:            messages,
		MaxCompletionTokens: 4096,
	})
	if err != nil {
		return "", fmt.Errorf("vision analysis failed: %w", err)
	}

	// Return the analysis result
	return fmt.Sprintf(`{"analysis":%q,"image":%q}`, resp.Content, imageURL), nil
}

// inferMimeTypeFromURL guesses the mime type from a URL's file extension.
func inferMimeTypeFromURL(imageURL string) string {
	parsed, err := url.Parse(imageURL)
	if err != nil {
		return "image/jpeg"
	}
	ext := strings.ToLower(filepath.Ext(parsed.Path))
	switch ext {
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	default:
		return "image/jpeg"
	}
}
