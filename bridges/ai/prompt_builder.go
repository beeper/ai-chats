package ai

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type historyReplayMode string

const (
	historyReplayNormal  historyReplayMode = "normal"
	historyReplayRegen   historyReplayMode = "regenerate"
	historyReplayRewrite historyReplayMode = "rewrite"
)

type historyReplayOptions struct {
	mode             historyReplayMode
	targetMessageID  networkid.MessageID
	excludeMessageID networkid.MessageID
}

type currentTurnTextOptions struct {
	rawEventContent  map[string]any
	includeLinkScope bool
	prepend          []string
	append           []string
}

type turnAttachmentOptions struct {
	mediaURL      string
	mimeType      string
	encryptedFile *event.EncryptedFileInfo
	mediaType     pendingMessageType
}

type currentTurnPromptOptions struct {
	currentTurnTextOptions
	leadingBlocks []PromptBlock
	attachment    *turnAttachmentOptions
}

func joinPromptFragments(parts ...string) string {
	var filtered []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	return strings.TrimSpace(strings.Join(filtered, "\n\n"))
}

func (oc *AIClient) replayHistoryMessages(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	opts historyReplayOptions,
) ([]PromptMessage, error) {
	extra := 0
	if opts.mode == historyReplayRegen {
		extra = 2
	}
	historyLimit := oc.historyLimit(ctx, portal, meta)
	if historyLimit <= 0 {
		return nil, nil
	}
	if extra > 0 {
		historyLimit += extra
	}
	history, err := oc.loadAIHistoryMessagesFromTurns(ctx, portal, historyLimit)
	if err != nil {
		return nil, err
	}
	hr := historyLoadResult{
		rows:      history,
		hasVision: oc.getModelCapabilitiesForMeta(ctx, meta).SupportsVision,
		limit:     historyLimit,
	}
	type replayCandidate struct {
		row  *database.Message
		meta *MessageMetadata
	}

	candidates := make([]replayCandidate, 0, len(hr.rows))
	for _, row := range hr.rows {
		if opts.excludeMessageID != "" && row.ID == opts.excludeMessageID {
			continue
		}
		msgMeta := messageMeta(row)
		if opts.mode == historyReplayRewrite && row.ID == opts.targetMessageID {
			candidates = append(candidates, replayCandidate{row: row, meta: msgMeta})
			continue
		}
		if !shouldIncludeInHistory(msgMeta) {
			continue
		}
		candidates = append(candidates, replayCandidate{row: row, meta: msgMeta})
	}

	skipUserID := networkid.MessageID("")
	skipAssistantID := networkid.MessageID("")
	if opts.mode == historyReplayRegen {
		for _, candidate := range candidates {
			if skipUserID == "" && candidate.meta != nil && candidate.meta.Role == string(PromptRoleUser) {
				skipUserID = candidate.row.ID
				continue
			}
			if skipAssistantID == "" && candidate.meta != nil && candidate.meta.Role == string(PromptRoleAssistant) {
				skipAssistantID = candidate.row.ID
			}
			if skipUserID != "" && skipAssistantID != "" {
				break
			}
		}
	}

	var messages []PromptMessage
	chatIndex := 0
	for i := len(candidates) - 1; i >= 0; i-- {
		candidate := candidates[i]
		if opts.mode == historyReplayRewrite && candidate.row.ID == opts.targetMessageID {
			break
		}
		if candidate.row.ID == skipUserID || candidate.row.ID == skipAssistantID {
			continue
		}
		injectImages := hr.hasVision && chatIndex < maxHistoryImageMessages
		bundle := oc.historyMessageBundle(ctx, candidate.meta, injectImages)
		if len(bundle) == 0 {
			continue
		}
		messages = append(messages, bundle...)
		chatIndex++
	}
	return messages, nil
}

func (oc *AIClient) buildPromptContextForTurn(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	userText string,
	eventID id.EventID,
	opts currentTurnPromptOptions,
) (PromptContext, error) {
	appendFragments := slices.Clone(opts.append)
	leadingBlocks := slices.Clone(opts.leadingBlocks)

	if opts.attachment != nil {
		switch opts.attachment.mediaType {
		case pendingTypeImage:
			b64Data, actualMimeType, err := oc.downloadMediaBase64(ctx, opts.attachment.mediaURL, opts.attachment.encryptedFile, 20, opts.attachment.mimeType)
			if err != nil {
				return PromptContext{}, fmt.Errorf("failed to download image: %w", err)
			}
			leadingBlocks = append(leadingBlocks, PromptBlock{
				Type:     PromptBlockImage,
				ImageB64: b64Data,
				MimeType: actualMimeType,
			})
		case pendingTypePDF:
			content, truncated, err := oc.downloadPDFFile(ctx, opts.attachment.mediaURL, opts.attachment.encryptedFile, opts.attachment.mimeType)
			if err != nil {
				return PromptContext{}, fmt.Errorf("failed to download PDF: %w", err)
			}
			filename := resolveMediaFileName("document.pdf", "pdf", opts.attachment.mediaURL)
			appendFragments = append(appendFragments, buildTextFileMessage("", false, filename, "application/pdf", content, truncated))
		case pendingTypeAudio:
			return PromptContext{}, fmt.Errorf("audio attachments must be preprocessed into text before prompt assembly")
		case pendingTypeVideo:
			return PromptContext{}, fmt.Errorf("video attachments must be preprocessed into text before prompt assembly")
		default:
			return PromptContext{}, fmt.Errorf("unsupported media type: %s", opts.attachment.mediaType)
		}
	}

	textOpts := opts.currentTurnTextOptions
	textOpts.append = appendFragments

	result, err := oc.prepareInboundPromptContext(ctx, portal, meta, userText, eventID)
	if err != nil {
		return PromptContext{}, err
	}
	prepend := slices.Clone(textOpts.prepend)
	if portal != nil && portal.MXID != "" {
		reactionFeedback := DrainReactionFeedback(portal.MXID)
		if len(reactionFeedback) > 0 {
			if feedbackText := FormatReactionFeedback(reactionFeedback); feedbackText != "" {
				prepend = append(prepend, feedbackText)
			}
		}
	}
	if result.UntrustedPrefix != "" {
		prepend = append(prepend, result.UntrustedPrefix)
	}
	appendParts := slices.Clone(textOpts.append)
	if textOpts.includeLinkScope {
		if linkContext := oc.buildLinkContext(ctx, userText, textOpts.rawEventContent); linkContext != "" {
			appendParts = append(appendParts, linkContext)
		}
	}
	text := joinPromptFragments(append(append(prepend, result.ResolvedBody), appendParts...)...)

	base := result.PromptContext
	blocks := make([]PromptBlock, 0, len(leadingBlocks)+1)
	blocks = append(blocks, leadingBlocks...)
	if strings.TrimSpace(text) != "" {
		blocks = append(blocks, PromptBlock{Type: PromptBlockText, Text: text})
	}
	base.Messages = append(base.Messages, PromptMessage{
		Role:   PromptRoleUser,
		Blocks: blocks,
	})
	return base, nil
}
