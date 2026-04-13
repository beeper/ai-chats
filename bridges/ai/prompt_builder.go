package ai

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
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

func (oc *AIClient) fetchHistoryRowsWithExtra(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	extra int,
) (*historyLoadResult, error) {
	historyLimit := oc.historyLimit(ctx, portal, meta)
	if historyLimit <= 0 {
		return nil, nil
	}
	if extra > 0 {
		historyLimit += extra
	}
	return &historyLoadResult{
		hasVision: oc.getModelCapabilitiesForMeta(ctx, meta).SupportsVision,
		limit:     historyLimit,
	}, nil
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
	hr, err := oc.fetchHistoryRowsWithExtra(ctx, portal, meta, extra)
	if err != nil {
		return nil, err
	}
	if hr == nil {
		return nil, nil
	}

	turns, err := loadAIPromptHistoryTurns(ctx, portal, hr.limit, opts)
	if err != nil {
		return nil, err
	}
	skipUserID := ""
	skipAssistantID := ""
	if opts.mode == historyReplayRegen {
		for _, turn := range turns {
			if skipUserID == "" && strings.TrimSpace(turn.Role) == string(PromptRoleUser) {
				skipUserID = turn.TurnID
				continue
			}
			if skipAssistantID == "" && strings.TrimSpace(turn.Role) == string(PromptRoleAssistant) {
				skipAssistantID = turn.TurnID
			}
			if skipUserID != "" && skipAssistantID != "" {
				break
			}
		}
	}

	var messages []PromptMessage
	chatIndex := 0
	for i := len(turns) - 1; i >= 0; i-- {
		turn := turns[i]
		if turn.TurnID == skipUserID || turn.TurnID == skipAssistantID {
			continue
		}
		injectImages := hr.hasVision && turn.Kind == aiTurnKindConversation && chatIndex < maxHistoryImageMessages
		bundle := filterPromptMessagesForHistory(promptMessagesFromTurnData(turn.TurnData), injectImages)
		if len(bundle) == 0 {
			continue
		}
		messages = append(messages, bundle...)
		if turn.Kind == aiTurnKindConversation {
			chatIndex++
		}
	}
	return messages, nil
}

func (oc *AIClient) buildCurrentTurnText(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	userText string,
	eventID id.EventID,
	opts currentTurnTextOptions,
) (PromptContext, string, error) {
	result, err := oc.prepareInboundPromptContext(ctx, portal, meta, userText, eventID)
	if err != nil {
		return PromptContext{}, "", err
	}

	prepend := slices.Clone(opts.prepend)
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

	appendParts := slices.Clone(opts.append)
	if opts.includeLinkScope {
		if linkContext := oc.buildLinkContext(ctx, userText, opts.rawEventContent); linkContext != "" {
			appendParts = append(appendParts, linkContext)
		}
	}

	body := joinPromptFragments(append(append(prepend, result.ResolvedBody), appendParts...)...)
	return result.PromptContext, body, nil
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
		attachmentBlocks, attachmentAppend, err := oc.normalizeTurnAttachment(ctx, *opts.attachment)
		if err != nil {
			return PromptContext{}, err
		}
		leadingBlocks = append(leadingBlocks, attachmentBlocks...)
		appendFragments = append(appendFragments, attachmentAppend...)
	}

	textOpts := opts.currentTurnTextOptions
	textOpts.append = appendFragments
	base, text, err := oc.buildCurrentTurnText(ctx, portal, meta, userText, eventID, textOpts)
	if err != nil {
		return PromptContext{}, err
	}

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

func (oc *AIClient) normalizeTurnAttachment(ctx context.Context, opts turnAttachmentOptions) ([]PromptBlock, []string, error) {
	switch opts.mediaType {
	case pendingTypeImage:
		b64Data, actualMimeType, err := oc.downloadMediaBase64(ctx, opts.mediaURL, opts.encryptedFile, 20, opts.mimeType)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to download image: %w", err)
		}
		return []PromptBlock{{
			Type:     PromptBlockImage,
			ImageB64: b64Data,
			MimeType: actualMimeType,
		}}, nil, nil
	case pendingTypePDF:
		content, truncated, err := oc.downloadPDFFile(ctx, opts.mediaURL, opts.encryptedFile, opts.mimeType)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to download PDF: %w", err)
		}
		filename := resolveMediaFileName("document.pdf", "pdf", opts.mediaURL)
		return nil, []string{buildTextFileMessage("", false, filename, "application/pdf", content, truncated)}, nil
	case pendingTypeAudio:
		return nil, nil, fmt.Errorf("audio attachments must be preprocessed into text before prompt assembly")
	case pendingTypeVideo:
		return nil, nil, fmt.Errorf("video attachments must be preprocessed into text before prompt assembly")
	default:
		return nil, nil, fmt.Errorf("unsupported media type: %s", opts.mediaType)
	}
}

func (oc *AIClient) buildCurrentTurnWithLinks(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	userText string,
	rawEventContent map[string]any,
	eventID id.EventID,
) (PromptContext, error) {
	return oc.buildPromptContextForTurn(ctx, portal, meta, userText, eventID, currentTurnPromptOptions{
		currentTurnTextOptions: currentTurnTextOptions{
			rawEventContent:  rawEventContent,
			includeLinkScope: true,
		},
	})
}

func (oc *AIClient) buildHeartbeatTurnContext(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt string,
) (PromptContext, error) {
	return oc.buildPromptContextForTurn(ctx, portal, meta, prompt, "", currentTurnPromptOptions{})
}
