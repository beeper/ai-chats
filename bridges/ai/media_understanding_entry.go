package ai

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (oc *AIClient) runMediaUnderstandingEntries(
	ctx context.Context,
	capability MediaUnderstandingCapability,
	attachment mediaAttachment,
	entries []MediaUnderstandingModelConfig,
	capCfg *MediaUnderstandingConfig,
) (*MediaUnderstandingOutput, []MediaUnderstandingModelDecision, error) {
	attempts := make([]MediaUnderstandingModelDecision, 0, len(entries))
	var lastErr error
	for _, entry := range entries {
		entryType := entry.ResolvedType()
		provider := strings.TrimSpace(entry.Provider)
		model := strings.TrimSpace(entry.Model)
		if entryType == MediaEntryTypeCLI {
			provider = strings.TrimSpace(entry.Command)
			if provider == "" {
				provider = string(MediaEntryTypeCLI)
			}
			if model == "" {
				model = provider
			}
		} else {
			provider = normalizeMediaProviderID(provider)
		}
		output, err := oc.runMediaUnderstandingEntry(ctx, capability, attachment, entry, capCfg)
		if err != nil {
			lastErr = err
			attempts = append(attempts, MediaUnderstandingModelDecision{
				Type:     entryType,
				Provider: provider,
				Model:    model,
				Outcome:  MediaOutcomeFailed,
				Reason:   err.Error(),
			})
			continue
		}
		if output == nil || strings.TrimSpace(output.Text) == "" {
			attempts = append(attempts, MediaUnderstandingModelDecision{
				Type:     entryType,
				Provider: provider,
				Model:    model,
				Outcome:  MediaOutcomeSkipped,
				Reason:   "empty output",
			})
			continue
		}
		attempts = append(attempts, MediaUnderstandingModelDecision{
			Type:     entryType,
			Provider: provider,
			Model:    model,
			Outcome:  MediaOutcomeSuccess,
		})
		return output, attempts, nil
	}
	return nil, attempts, lastErr
}

func filterMediaOutputs(outputs []MediaUnderstandingOutput, kind MediaUnderstandingKind) []MediaUnderstandingOutput {
	filtered := make([]MediaUnderstandingOutput, 0, len(outputs))
	for _, output := range outputs {
		if output.Kind == kind {
			filtered = append(filtered, output)
		}
	}
	return filtered
}

func (oc *AIClient) extractMediaFileBlocks(
	ctx context.Context,
	attachments []mediaAttachment,
	outputs []MediaUnderstandingOutput,
) []string {
	if len(attachments) == 0 {
		return nil
	}
	skip := map[int]bool{}
	for _, output := range outputs {
		if output.Kind == MediaKindAudioTranscription {
			skip[output.AttachmentIndex] = true
		}
	}
	var blocks []string
	for _, attachment := range attachments {
		if skip[attachment.Index] {
			continue
		}
	}
	return blocks
}

func (oc *AIClient) runMediaUnderstandingEntry(
	ctx context.Context,
	capability MediaUnderstandingCapability,
	attachment mediaAttachment,
	entry MediaUnderstandingModelConfig,
	capCfg *MediaUnderstandingConfig,
) (*MediaUnderstandingOutput, error) {
	entryType := entry.ResolvedType()

	maxChars := resolveMediaMaxChars(capability, entry, capCfg)
	maxBytes := resolveMediaMaxBytes(capability, entry, capCfg)
	prompt := resolveMediaPrompt(capability, entry.Prompt, capCfg, maxChars)
	timeout := resolveMediaTimeoutSeconds(entry.TimeoutSeconds, capCfg, defaultTimeoutSecondsByCapability[capability])

	switch entryType {
	case MediaEntryTypeCLI:
		data, actualMime, err := oc.downloadMediaBytes(ctx, attachment.URL, attachment.EncryptedFile, maxBytes, attachment.MimeType)
		if err != nil {
			return nil, err
		}
		fileName := resolveMediaFileName(attachment.FileName, string(capability), attachment.URL)
		tempDir, err := os.MkdirTemp("", "aichats-media-*")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tempDir)
		mediaPath := filepath.Join(tempDir, fileName)
		if err := os.WriteFile(mediaPath, data, 0600); err != nil {
			return nil, err
		}
		if actualMime != "" {
			attachment.MimeType = actualMime
		}
		output, err := runMediaCLI(ctx, entry.Command, entry.Args, prompt, maxChars, mediaPath)
		if err != nil {
			return nil, err
		}
		return buildMediaOutput(capability, output, string(MediaEntryTypeCLI), entry.Model, attachment.Index), nil

	default:
		providerID := normalizeMediaProviderID(entry.Provider)
		if providerID == "" && capability != MediaCapabilityImage {
			return nil, fmt.Errorf("missing provider for %s understanding", capability)
		}

		switch capability {
		case MediaCapabilityImage:
			return oc.describeImageWithEntry(ctx, entry, capCfg, attachment.URL, attachment.MimeType, attachment.EncryptedFile, maxBytes, maxChars, prompt, attachment.Index)
		case MediaCapabilityAudio:
			return oc.transcribeAudioWithEntry(ctx, entry, capCfg, attachment.URL, attachment.MimeType, attachment.EncryptedFile, attachment.FileName, maxBytes, maxChars, prompt, timeout, attachment.Index)
		case MediaCapabilityVideo:
			return oc.describeVideoWithEntry(ctx, entry, capCfg, attachment.URL, attachment.MimeType, attachment.EncryptedFile, maxBytes, maxChars, prompt, timeout, attachment.Index)
		}
	}
	return nil, fmt.Errorf("unsupported media capability %s", capability)
}

func buildMediaOutput(capability MediaUnderstandingCapability, text string, provider string, model string, attachmentIndex int) *MediaUnderstandingOutput {
	kind := MediaKindImageDescription
	switch capability {
	case MediaCapabilityAudio:
		kind = MediaKindAudioTranscription
	case MediaCapabilityVideo:
		kind = MediaKindVideoDescription
	}
	return &MediaUnderstandingOutput{
		Kind:            kind,
		AttachmentIndex: attachmentIndex,
		Text:            strings.TrimSpace(text),
		Provider:        strings.TrimSpace(provider),
		Model:           strings.TrimSpace(model),
	}
}
