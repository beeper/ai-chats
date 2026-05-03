package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
)

// HandleMatrixEdit handles edits to previously sent messages
func (oc *AIClient) HandleMatrixEdit(ctx context.Context, edit *bridgev2.MatrixEdit) error {
	portal := edit.Portal
	if portal == nil {
		return errors.New("portal is nil")
	}
	var err error
	portal, err = resolvePortalForAIDB(ctx, oc, portal)
	if err != nil {
		return fmt.Errorf("failed to canonicalize portal for edit: %w", err)
	}
	edit.Portal = portal
	meta := portalMeta(portal)
	if meta != nil && meta.ResolvedTarget != nil && meta.ResolvedTarget.Kind == ResolvedTargetModel {
		return bridgev2.ErrEditsNotSupportedInPortal
	}
	if edit.Content == nil || edit.EditTarget == nil {
		return errors.New("invalid edit: missing content or target")
	}

	// Get the new message body
	newBody := strings.TrimSpace(edit.Content.Body)
	if newBody == "" {
		return errors.New("empty edit body")
	}

	// Update the message metadata with the new content
	msgMeta := messageMeta(edit.EditTarget)
	if msgMeta == nil {
		msgMeta = &MessageMetadata{}
		edit.EditTarget.Metadata = msgMeta
	}
	transcriptMsg, err := oc.loadAIConversationMessage(ctx, portal, edit.EditTarget.ID, edit.EditTarget.MXID)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to load edited conversation turn")
	}
	if transcriptMsg == nil {
		transcriptMsg = cloneMessageForAIHistory(edit.EditTarget)
	}
	transcriptMeta, ok := transcriptMsg.Metadata.(*MessageMetadata)
	if !ok || transcriptMeta == nil {
		transcriptMeta = cloneMessageMetadata(msgMeta)
		if transcriptMeta == nil {
			transcriptMeta = &MessageMetadata{}
		}
		transcriptMsg.Metadata = transcriptMeta
	}
	transcriptMeta.Body = newBody
	role := strings.TrimSpace(transcriptMeta.Role)
	if role == "" {
		role = strings.TrimSpace(msgMeta.Role)
	}
	if role == "user" {
		if _, turnData, ok := buildUserPromptTurn([]PromptBlock{{
			Type: PromptBlockText,
			Text: newBody,
		}}); ok {
			transcriptMeta.CanonicalTurnData = turnData.ToMap()
		} else {
			transcriptMeta.CanonicalTurnData = nil
		}
	} else {
		transcriptMeta.CanonicalTurnData = nil
	}
	if err := oc.persistAIConversationMessage(ctx, portal, transcriptMsg); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to persist edited conversation turn")
		return err
	}
	if edit.EditTarget != nil {
		edit.EditTarget.Metadata = cloneMessageMetadata(transcriptMeta)
	}
	// Only regenerate if this was a user message
	if role != "user" {
		// Just update the content, don't regenerate
		return nil
	}

	oc.loggerForContext(ctx).Info().
		Str("message_id", string(edit.EditTarget.ID)).
		Int("new_body_len", len(newBody)).
		Msg("User edited message, regenerating response")

	// Find the assistant response that came after this message
	// We'll delete it and regenerate
	err = oc.regenerateFromEdit(ctx, edit.Event, portal, meta, edit.EditTarget, newBody)
	if err != nil {
		oc.loggerForContext(ctx).Err(err).Msg("Failed to regenerate response after edit")
		oc.sendSystemNotice(ctx, portal, fmt.Sprintf("Couldn't regenerate the response: %v", err))
	}

	return nil
}

// regenerateFromEdit regenerates the AI response based on an edited user message
func (oc *AIClient) regenerateFromEdit(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	editedMessage *database.Message,
	newBody string,
) error {
	// Get messages in the portal to find the assistant response after the edited message
	messages, err := oc.getAIHistoryMessages(ctx, portal, 50)
	if err != nil {
		return fmt.Errorf("failed to get messages: %w", err)
	}

	// Find the assistant response that came after the edited message
	// Messages come newest-first from GetLastNInPortal, so lower indices are newer
	var assistantResponse *database.Message

	// First find the index of the edited message
	editedIdx := -1
	for i, msg := range messages {
		if msg.ID == editedMessage.ID {
			editedIdx = i
			break
		}
	}

	if editedIdx > 0 {
		// Search toward newer messages (lower indices) for assistant response
		for i := editedIdx - 1; i >= 0; i-- {
			msgMeta := messageMeta(messages[i])
			if msgMeta != nil && msgMeta.Role == "assistant" {
				assistantResponse = messages[i]
				break
			}
		}
	}

	pending := pendingMessage{
		Event:       snapshotPendingEvent(evt),
		Portal:      portal,
		Meta:        meta,
		Type:        pendingTypeEditRegenerate,
		MessageBody: newBody,
		TargetMsgID: editedMessage.ID,
		Typing: &TypingContext{
			IsGroup:      oc.isGroupChat(ctx, portal),
			WasMentioned: true,
		},
	}
	// Build the prompt with the edited message included.
	promptContext, err := oc.buildPromptContextForPendingMessage(ctx, pending, "")
	if err != nil {
		return fmt.Errorf("failed to build prompt: %w", err)
	}

	// If we found an assistant response, we'll redact/edit it
	if assistantResponse != nil {
		// Try to redact the old response
		if assistantResponse.MXID != "" {
			_ = oc.redactEventViaPortal(ctx, portal, assistantResponse.MXID)
		}
	}

	var cfg *Config
	if oc != nil && oc.connector != nil {
		cfg = &oc.connector.Config
	}
	queueSettings := resolveQueueSettings(queueResolveParams{cfg: cfg, channel: "matrix", inlineOpts: airuntime.QueueInlineOptions{}})
	queueItem := pendingQueueItem{
		pending:     pending,
		messageID:   string(evt.ID),
		summaryLine: newBody,
		enqueuedAt:  time.Now().UnixMilli(),
	}
	return oc.dispatchOrQueueCore(ctx, pending.Event, portal, meta, queueItem, queueSettings, promptContext)
}

// buildPromptForRegenerate builds a prompt for regeneration, excluding the last assistant message
func (oc *AIClient) buildContextForRegenerate(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	latestUserBody string,
) (PromptContext, error) {
	base := PromptContext{
		SystemPrompt: oc.buildConversationSystemPromptText(ctx, portal, meta, false),
	}
	historyMessages, err := oc.replayHistoryMessages(ctx, portal, meta, historyReplayOptions{mode: historyReplayRegen})
	if err != nil {
		return PromptContext{}, err
	}
	base.Messages = append(base.Messages, historyMessages...)
	if userMessage, turnData, ok := buildUserPromptTurn([]PromptBlock{{
		Type: PromptBlockText,
		Text: latestUserBody,
	}}); ok {
		base.Messages = append(base.Messages, userMessage)
		base.CurrentTurnData = turnData
	}
	return base, nil
}
