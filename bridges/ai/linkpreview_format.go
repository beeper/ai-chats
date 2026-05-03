package ai

import (
	"context"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	_ "golang.org/x/image/webp"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func UploadPreviewImages(ctx context.Context, previews []*PreviewWithImage, intent bridgev2.MatrixAPI, roomID id.RoomID) []*event.BeeperLinkPreview {
	if len(previews) == 0 {
		return nil
	}

	result := make([]*event.BeeperLinkPreview, 0, len(previews))
	for _, p := range previews {
		if p == nil || p.Preview == nil {
			continue
		}

		preview := cloneBeeperLinkPreview(p.Preview)
		if preview == nil {
			continue
		}

		// Upload image if we have data
		if len(p.ImageData) > 0 && intent != nil {
			uri, file, err := intent.UploadMedia(ctx, roomID, p.ImageData, "", preview.ImageType)
			if err == nil {
				preview.ImageURL = uri
				preview.ImageEncryption = file
			}
		}

		result = append(result, preview)
	}

	return result
}

// ExtractBeeperPreviews extracts just the BeeperLinkPreview from PreviewWithImage slice.
func ExtractBeeperPreviews(previews []*PreviewWithImage) []*event.BeeperLinkPreview {
	if len(previews) == 0 {
		return nil
	}

	result := make([]*event.BeeperLinkPreview, 0, len(previews))
	for _, p := range previews {
		if p != nil && p.Preview != nil {
			result = append(result, p.Preview)
		}
	}
	return result
}

// FormatPreviewsForContext formats link previews for injection into LLM context.
func FormatPreviewsForContext(previews []*event.BeeperLinkPreview, maxChars int) string {
	if len(previews) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n[Referenced Links]\n")

	for i, p := range previews {
		if p == nil {
			continue
		}

		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, p.MatchedURL))
		if p.Title != "" {
			sb.WriteString(fmt.Sprintf("   Title: %s\n", p.Title))
		}
		if p.Description != "" {
			desc := p.Description
			if len(desc) > maxChars {
				desc = desc[:maxChars] + "..."
			}
			sb.WriteString(fmt.Sprintf("   Description: %s\n", desc))
		}
		if p.SiteName != "" {
			sb.WriteString(fmt.Sprintf("   Site: %s\n", p.SiteName))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// ParseExistingLinkPreviews extracts link previews from a Matrix event's raw content.
func ParseExistingLinkPreviews(rawContent map[string]any) []*event.BeeperLinkPreview {
	previewsRaw, ok := existingLinkPreviewsRaw(rawContent)
	if !ok {
		return nil
	}

	previewsList, ok := previewsRaw.([]any)
	if !ok {
		return nil
	}

	var previews []*event.BeeperLinkPreview
	for _, p := range previewsList {
		previewMap, ok := p.(map[string]any)
		if !ok {
			continue
		}

		preview := &event.BeeperLinkPreview{}

		if v, ok := previewMap["matched_url"].(string); ok {
			preview.MatchedURL = v
		}
		if v, ok := previewMap["og:url"].(string); ok {
			preview.CanonicalURL = v
		}
		if v, ok := previewMap["og:title"].(string); ok {
			preview.Title = v
		}
		if v, ok := previewMap["og:description"].(string); ok {
			preview.Description = v
		}
		if v, ok := previewMap["og:type"].(string); ok {
			preview.Type = v
		}
		if v, ok := previewMap["og:site_name"].(string); ok {
			preview.SiteName = v
		}
		if v, ok := previewMap["og:image"].(string); ok {
			preview.ImageURL = id.ContentURIString(v)
		}

		if preview.MatchedURL != "" || preview.CanonicalURL != "" {
			previews = append(previews, preview)
		}
	}

	return previews
}

func existingLinkPreviewsRaw(rawContent map[string]any) (any, bool) {
	if rawContent == nil {
		return nil, false
	}
	if newContent, ok := rawContent["m.new_content"].(map[string]any); ok && newContent != nil {
		if previewsRaw, ok := newContent["com.beeper.linkpreviews"]; ok {
			return previewsRaw, true
		}
	}
	previewsRaw, ok := rawContent["com.beeper.linkpreviews"]
	return previewsRaw, ok
}

// PreviewsToMapSlice converts BeeperLinkPreviews to a format suitable for JSON serialization.
func PreviewsToMapSlice(previews []*event.BeeperLinkPreview) []map[string]any {
	if len(previews) == 0 {
		return nil
	}

	result := make([]map[string]any, 0, len(previews))
	for _, p := range previews {
		if p == nil {
			continue
		}

		m := map[string]any{
			"matched_url": p.MatchedURL,
		}
		if p.CanonicalURL != "" {
			m["og:url"] = p.CanonicalURL
		}
		if p.Title != "" {
			m["og:title"] = p.Title
		}
		if p.Description != "" {
			m["og:description"] = p.Description
		}
		if p.Type != "" {
			m["og:type"] = p.Type
		}
		if p.SiteName != "" {
			m["og:site_name"] = p.SiteName
		}
		if p.ImageURL != "" {
			m["og:image"] = string(p.ImageURL)
		}
		if p.ImageType != "" {
			m["og:image:type"] = p.ImageType
		}
		if p.ImageWidth != 0 {
			m["og:image:width"] = int(p.ImageWidth)
		}
		if p.ImageHeight != 0 {
			m["og:image:height"] = int(p.ImageHeight)
		}
		if p.ImageSize != 0 {
			m["matrix:image:size"] = int(p.ImageSize)
		}
		if p.ImageEncryption != nil {
			m["beeper:image:encryption"] = p.ImageEncryption
		}

		result = append(result, m)
	}
	return result
}
