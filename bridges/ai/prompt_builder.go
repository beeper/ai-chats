package ai

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

type historyReplayMode string

const (
	historyReplayNormal  historyReplayMode = "normal"
	historyReplayRegen   historyReplayMode = "regenerate"
	historyReplayRewrite historyReplayMode = "rewrite"
)

type historyReplayOptions struct {
	mode            historyReplayMode
	targetMessageID networkid.MessageID
}

type currentTurnTextOptions struct {
	rawEventContent  map[string]any
	includeLinkScope bool
	prepend          []string
	append           []string
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
	resetAt := int64(0)
	if meta != nil {
		resetAt = meta.SessionResetAt
	}
	history, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, historyLimit)
	if err != nil {
		return nil, err
	}
	return &historyLoadResult{
		rows:      history,
		hasVision: oc.getModelCapabilitiesForMeta(ctx, meta).SupportsVision,
		resetAt:   resetAt,
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

	type replayCandidate struct {
		row  *database.Message
		meta *MessageMetadata
	}

	candidates := make([]replayCandidate, 0, len(hr.rows))
	for _, row := range hr.rows {
		msgMeta := messageMeta(row)
		if opts.mode == historyReplayRewrite && row.ID == opts.targetMessageID {
			candidates = append(candidates, replayCandidate{row: row, meta: msgMeta})
			continue
		}
		if !shouldIncludeInHistory(msgMeta) {
			continue
		}
		if hr.resetAt > 0 && row.Timestamp.UnixMilli() < hr.resetAt {
			continue
		}
		candidates = append(candidates, replayCandidate{row: row, meta: msgMeta})
	}

	skipUserID := networkid.MessageID("")
	skipAssistantID := networkid.MessageID("")
	if opts.mode == historyReplayRegen {
		for _, candidate := range candidates {
			if skipUserID == "" && candidate.meta != nil && candidate.meta.Role == "user" {
				skipUserID = candidate.row.ID
				continue
			}
			if skipAssistantID == "" && candidate.meta != nil && candidate.meta.Role == "assistant" {
				skipAssistantID = candidate.row.ID
			}
			if skipUserID != "" && skipAssistantID != "" {
				break
			}
		}
	}

	var messages []PromptMessage
	for i := len(candidates) - 1; i >= 0; i-- {
		candidate := candidates[i]
		if opts.mode == historyReplayRewrite && candidate.row.ID == opts.targetMessageID {
			break
		}
		if candidate.row.ID == skipUserID || candidate.row.ID == skipAssistantID {
			continue
		}
		injectImages := hr.hasVision && i < maxHistoryImageMessages
		messages = append(messages, oc.historyMessageBundle(ctx, candidate.meta, injectImages)...)
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

	prepend := append([]string{}, opts.prepend...)
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

	appendParts := append([]string{}, opts.append...)
	if opts.includeLinkScope {
		if linkContext := oc.buildLinkContext(ctx, userText, opts.rawEventContent); linkContext != "" {
			appendParts = append(appendParts, linkContext)
		}
	}

	body := joinPromptFragments(append(append(prepend, result.ResolvedBody), appendParts...)...)
	return result.PromptContext, body, nil
}
