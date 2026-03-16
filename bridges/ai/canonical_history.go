package ai

import (
	"context"
	"fmt"
	"strings"
)

func (oc *AIClient) historyMessageBundle(
	ctx context.Context,
	msgMeta *MessageMetadata,
	injectImages bool,
) []PromptMessage {
	if msgMeta == nil {
		return nil
	}
	if canonical := filterPromptMessagesForHistory(promptMessagesFromMetadata(msgMeta), injectImages); len(canonical) > 0 {
		if injectImages && len(msgMeta.GeneratedFiles) > 0 {
			if generated := oc.generatedImagesHistoryMessage(ctx, msgMeta.GeneratedFiles); len(generated.Blocks) > 0 {
				return append(canonical, generated)
			}
		}
		return canonical
	}

	return nil
}

func (oc *AIClient) generatedImagesHistoryMessage(ctx context.Context, files []GeneratedFileRef) PromptMessage {
	if len(files) == 0 {
		return PromptMessage{}
	}
	blocks := make([]PromptBlock, 0, 1+len(files))
	var sb strings.Builder
	sb.WriteString("[Previously generated image(s) for reference]")
	for _, f := range files {
		if !isImageMimeType(f.MimeType) || strings.TrimSpace(f.URL) == "" {
			continue
		}
		fmt.Fprintf(&sb, "\n[media_url: %s]", f.URL)
		if imgPart := oc.downloadHistoryImageBlock(ctx, f.URL, f.MimeType); imgPart != nil {
			blocks = append(blocks, *imgPart)
		}
	}
	if len(blocks) == 0 {
		return PromptMessage{}
	}
	blocks = append([]PromptBlock{{
		Type: PromptBlockText,
		Text: sb.String(),
	}}, blocks...)
	return PromptMessage{
		Role:   PromptRoleUser,
		Blocks: blocks,
	}
}

func (oc *AIClient) downloadHistoryImageBlock(ctx context.Context, mediaURL, mimeType string) *PromptBlock {
	b64Data, actualMimeType, err := oc.downloadMediaBase64(ctx, mediaURL, nil, 25, mimeType)
	if err != nil {
		oc.log.Debug().Err(err).Str("url", mediaURL).Msg("Failed to download history image, skipping")
		return nil
	}
	return &PromptBlock{
		Type:     PromptBlockImage,
		ImageB64: b64Data,
		MimeType: actualMimeType,
	}
}
