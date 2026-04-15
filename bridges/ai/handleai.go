package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/rs/zerolog"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
)

func (oc *AIClient) notifyMatrixSendFailure(ctx context.Context, portal *bridgev2.Portal, evt *event.Event, err error) {
	if bridgeState, shouldMarkLoggedOut, ok := bridgeStateForError(err); ok {
		if shouldMarkLoggedOut {
			oc.SetLoggedIn(false)
		}
		oc.UserLogin.BridgeState.Send(bridgeState)
	}

	if portal == nil || portal.Bridge == nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to send message via OpenAI")
		return
	}

	// Use FormatUserFacingError for consistent, user-friendly error messages
	errorMessage := FormatUserFacingError(err)

	if evt != nil {
		status := messageStatusForError(err)
		reason := messageStatusReasonForError(err)

		msgStatus := bridgev2.WrapErrorInStatus(err).
			WithStatus(status).
			WithErrorReason(reason).
			WithMessage(errorMessage).
			WithIsCertain(true).
			WithSendNotice(true)
		if portal != nil && portal.Bridge != nil {
			if info := bridgev2.StatusEventInfoFromEvent(evt); info != nil {
				portal.Bridge.Matrix.SendMessageStatus(ctx, &msgStatus, info)
			}
		}
		for _, extra := range statusEventsFromContext(ctx) {
			if extra != nil {
				if portal != nil && portal.Bridge != nil {
					if info := bridgev2.StatusEventInfoFromEvent(extra); info != nil {
						portal.Bridge.Matrix.SendMessageStatus(ctx, &msgStatus, info)
					}
				}
			}
		}
	}

	// Some clients don't surface message status errors, so also send a notice.
	oc.sendSystemNotice(ctx, portal, fmt.Sprintf("Couldn't complete the request: %s", errorMessage))

	// Track consecutive failures for provider health monitoring
	oc.recordProviderError(ctx)
}

func bridgeStateForError(err error) (status.BridgeState, bool, bool) {
	if err == nil {
		return status.BridgeState{}, false, false
	}

	if IsAuthError(err) {
		return status.BridgeState{
			StateEvent: status.StateBadCredentials,
			Error:      AIAuthFailed,
			Message:    "Authentication failed. Sign in again.",
			Info: map[string]any{
				"error": err.Error(),
			},
		}, true, true
	}

	if IsPermissionDeniedError(err) {
		return status.BridgeState{
			StateEvent: status.StateUnknownError,
			Error:      AIProviderError,
			Message:    FormatUserFacingError(err),
			Info: map[string]any{
				"error": err.Error(),
			},
		}, false, true
	}

	if IsBillingError(err) {
		return status.BridgeState{
			StateEvent: status.StateTransientDisconnect,
			Error:      AIBillingError,
			Message:    "There's a billing issue with the AI provider. Check your account or credits.",
		}, false, true
	}

	if IsRateLimitError(err) || IsOverloadedError(err) {
		return status.BridgeState{
			StateEvent: status.StateTransientDisconnect,
			Error:      AIRateLimited,
			Message:    "You're sending requests too quickly. Wait a moment, then try again.",
		}, false, true
	}

	return status.BridgeState{}, false, false
}

const healthWarningThreshold = 5

// recordProviderError increments the consecutive error counter and escalates to a
// bridge state warning after repeated failures.
func (oc *AIClient) recordProviderError(ctx context.Context) {
	var nextErrors int
	var crossedThreshold bool
	_ = oc.updateLoginState(ctx, func(state *loginRuntimeState) bool {
		nextErrors, crossedThreshold = state.RecordProviderError(time.Now(), healthWarningThreshold)
		return true
	})
	if crossedThreshold {
		oc.UserLogin.BridgeState.Send(status.BridgeState{
			StateEvent: status.StateTransientDisconnect,
			Error:      AIProviderError,
			Message:    fmt.Sprintf("The AI provider failed %d requests in a row", nextErrors),
		})
	}
}

func (oc *AIClient) recordProviderSuccess(ctx context.Context) {
	var recovered bool
	_ = oc.updateLoginState(ctx, func(state *loginRuntimeState) bool {
		recovered = state.RecordProviderSuccess(healthWarningThreshold)
		return recovered
	})
	if recovered && oc.IsLoggedIn() {
		oc.UserLogin.BridgeState.Send(status.BridgeState{
			StateEvent: status.StateConnected,
			Message:    "Connected",
		})
	}
}

func (oc *AIClient) setModelTyping(ctx context.Context, portal *bridgev2.Portal, typing bool) {
	if portal == nil || portal.MXID == "" {
		return
	}
	_, intent, err := oc.resolvePortalSenderAndIntent(ctx, portal, bridgev2.RemoteEventMessage, typing)
	if err != nil || intent == nil {
		return
	}
	var timeout time.Duration
	if typing {
		timeout = 30 * time.Second
	} else {
		timeout = 0 // Zero timeout stops typing
	}
	if err := intent.MarkTyping(ctx, portal.MXID, bridgev2.TypingTypeText, timeout); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		oc.loggerForContext(ctx).Warn().Err(err).Bool("typing", typing).Msg("Failed to set typing indicator")
	}
}

const autoGreetingDelay = 5 * time.Second

func (oc *AIClient) hasPortalMessages(ctx context.Context, portal *bridgev2.Portal) bool {
	if oc == nil || portal == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.DB == nil {
		return true
	}

	// Use a small lookback window so we can ignore "non-user" internal messages (e.g. welcome notices,
	// subagent triggers) when deciding whether the chat is "empty enough" to auto-greet.
	history, err := oc.getAIHistoryMessages(ctx, portal, 10)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to check portal message history")
		// Best-effort: if the DB is temporarily unavailable, prefer still scheduling the greeting.
		// The goroutine re-checks message history before dispatching.
		return false
	}
	for _, msg := range history {
		meta, ok := msg.Metadata.(*MessageMetadata)
		if !ok || meta == nil {
			// Some bridge-generated events may be stored with nil/unknown metadata (e.g. notices/state echoes).
			// Only treat them as "conversation has started" if they look like user/assistant messages by sender.
			if msg.SenderID == humanUserID(oc.UserLogin.ID) {
				return true
			}
			if portal.OtherUserID != "" && msg.SenderID == portal.OtherUserID {
				return true
			}
			continue
		}
		if meta.ExcludeFromHistory {
			continue
		}
		role := strings.TrimSpace(strings.ToLower(meta.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		if strings.TrimSpace(meta.Body) == "" {
			continue
		}
		return true
	}
	return oc.hasInternalPromptHistory(ctx, portal)
}

func isInternalControlRoom(meta *PortalMetadata) bool {
	if meta == nil {
		return false
	}
	return meta.InternalRoom()
}

func autoGreetingBlockReason(meta *PortalMetadata) string {
	switch {
	case isInternalControlRoom(meta):
		return "internal-control-room"
	case resolveAgentID(meta) == "":
		return "no-agent"
	}
	return ""
}

func (oc *AIClient) scheduleAutoGreeting(ctx context.Context, portal *bridgev2.Portal) {
	if oc == nil || portal == nil || portal.MXID == "" {
		return
	}
	meta := portalMeta(portal)
	if autoGreetingBlockReason(meta) != "" {
		return
	}
	if oc.hasPortalMessages(ctx, portal) {
		return
	}

	portalKey := portal.PortalKey
	roomID := portal.MXID
	go func() {
		oc.log.Debug().Stringer("room_id", roomID).Msg("auto-greeting loop started")
		bgCtx := oc.backgroundContext(ctx)
		for {
			delay := autoGreetingDelay
			if roomID != "" {
				if state, ok := oc.getUserTypingState(roomID); ok && !state.lastActivity.IsZero() {
					if since := time.Since(state.lastActivity); since < autoGreetingDelay {
						delay = autoGreetingDelay - since
					}
				}
			}
			timer := time.NewTimer(delay)
			<-timer.C
			timer.Stop()

			current, err := oc.UserLogin.Bridge.GetPortalByKey(bgCtx, portalKey)
			if err != nil || current == nil {
				oc.log.Debug().Stringer("room_id", roomID).Msg("auto-greeting loop exiting: portal not found")
				return
			}
			currentMeta := portalMeta(current)
			if currentMeta != nil && currentMeta.AutoGreetingSent {
				oc.log.Debug().Stringer("room_id", roomID).Msg("auto-greeting loop exiting: already sent")
				return
			}
			if reason := autoGreetingBlockReason(currentMeta); reason != "" {
				oc.log.Debug().Stringer("room_id", roomID).Str("reason", reason).Msg("auto-greeting loop exiting: blocked by portal state")
				return
			}
			if oc.hasPortalMessages(bgCtx, current) {
				oc.log.Debug().Stringer("room_id", roomID).Msg("auto-greeting loop exiting: portal has messages")
				return
			}
			if oc.isUserTyping(current.MXID) || !oc.userIdleFor(current.MXID, autoGreetingDelay) {
				continue
			}

			currentMeta.AutoGreetingSent = true
			if err := oc.savePortal(bgCtx, current, "auto greeting state"); err != nil {
				oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to persist auto greeting state")
				return
			}
			if _, _, err := oc.dispatchInternalMessage(bgCtx, current, currentMeta, autoGreetingPrompt, "auto-greeting", true); err != nil {
				oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to dispatch auto greeting")
			}
			return
		}
	}()
}

func (oc *AIClient) sendDisclaimerNotice(ctx context.Context, portal *bridgev2.Portal) error {
	if oc == nil || portal == nil {
		return nil
	}
	portal, err := resolvePortalForAIDB(ctx, oc, portal)
	if err != nil {
		return err
	}
	// We can't send a disclaimer until the Matrix room exists.
	if portal.MXID == "" {
		return nil
	}
	if oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return nil
	}
	meta := portalMeta(portal)
	if meta == nil {
		return nil
	}
	if meta.InternalRoom() {
		return nil
	}
	if meta.DisclaimerSent {
		return nil
	}

	meta.DisclaimerSent = true
	bgCtx, cancel := context.WithTimeout(oc.backgroundContext(ctx), 10*time.Second)
	defer cancel()
	if err := oc.savePortal(bgCtx, portal, "disclaimer state"); err != nil {
		return fmt.Errorf("persist disclaimer state: %w", err)
	}

	var disclaimer string
	if resolveAgentID(meta) == "" {
		modelID := oc.effectiveModel(meta)
		displayName := modelContactName(modelID, oc.findModelInfo(modelID))
		disclaimer = fmt.Sprintf("You are chatting with %s. AI can make mistakes.", displayName)
	} else {
		disclaimer = "AI can make mistakes."
	}
	oc.log.Debug().Stringer("portal", portal.PortalKey).Msg("Sending disclaimer notice")
	if err := oc.sendSystemNoticeMessage(bgCtx, portal, disclaimer); err != nil {
		meta.DisclaimerSent = false
		if saveErr := oc.savePortal(bgCtx, portal, "disclaimer rollback"); saveErr != nil {
			oc.loggerForContext(ctx).Warn().Err(saveErr).Msg("Failed to roll back disclaimer state")
		}
		return fmt.Errorf("send disclaimer: %w", err)
	}

	portal.UpdateCapabilities(bgCtx, oc.UserLogin, true)
	return nil
}

func (oc *AIClient) maybeGenerateTitle(ctx context.Context, portal *bridgev2.Portal, assistantResponse string) {
	if oc == nil || portal == nil {
		return
	}
	portal, err := resolvePortalForAIDB(ctx, oc, portal)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to canonicalize portal for title generation")
		return
	}
	meta := portalMeta(portal)

	if !oc.isOpenRouterProvider() {
		return
	}

	// Skip if title was already generated
	if meta.TitleGenerated {
		return
	}

	// Generate title in background to not block the message flow
	go func() {
		// Use a bounded timeout to prevent goroutine leaks if the API blocks
		bgCtx, cancel := context.WithTimeout(oc.backgroundContext(ctx), 15*time.Second)
		defer cancel()

		// Fetch the last user message from database
		messages, err := oc.getAIHistoryMessages(bgCtx, portal, 10)
		if err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to get messages for title generation")
			return
		}

		var userMessage string
		for i := len(messages) - 1; i >= 0; i-- {
			msg := messages[i]
			msgMeta, ok := msg.Metadata.(*MessageMetadata)
			if ok && msgMeta != nil && msgMeta.Role == "user" && msgMeta.Body != "" {
				userMessage = msgMeta.Body
				break
			}
		}

		if userMessage == "" {
			oc.loggerForContext(ctx).Debug().Msg("No user message found for title generation")
			return
		}

		title, err := oc.generateRoomTitle(bgCtx, userMessage, assistantResponse)
		if err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to generate room title")
			return
		}

		if title == "" {
			return
		}

		meta := portalMeta(portal)
		if meta != nil {
			meta.TitleGenerated = true
		}
		if portal.MXID != "" {
			portal.UpdateInfo(bgCtx, &bridgev2.ChatInfo{
				Name:                       &title,
				ExcludeChangesFromTimeline: true,
			}, oc.UserLogin, nil, time.Time{})
		} else {
			portal.Name = title
			portal.NameSet = true
		}
		if err := oc.savePortal(bgCtx, portal, "room title"); err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to persist generated room title")
			return
		}
	}()
}

// Priority: UserLoginMetadata.TitleGenerationModel > provider-specific default > current model
// Uses Responses API for OpenRouter compatibility (the PDF plugins middleware adds a 'plugins'
// field that is only valid for Responses API, not Chat Completions API)
func (oc *AIClient) generateRoomTitle(ctx context.Context, userMessage, assistantResponse string) (string, error) {
	provider := loginMetadata(oc.UserLogin).Provider
	if provider != ProviderOpenRouter && provider != ProviderMagicProxy {
		return "", errors.New("title generation disabled for this provider")
	}
	cfg := oc.loginConfigSnapshot(context.Background())
	model := cfg.TitleGenerationModel
	if model == "" {
		model = "google/gemini-2.5-flash"
	}
	if model == "" {
		return "", errors.New("title generation disabled for this provider")
	}

	oc.loggerForContext(ctx).Debug().Str("model", model).Msg("Generating room title")

	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(model),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(fmt.Sprintf(
				"Generate a very short title (3-5 words max) that summarizes this conversation. Reply with ONLY the title, no quotes, no punctuation at the end.\n\nUser: %s\n\nAssistant: %s",
				userMessage, assistantResponse,
			)),
		},
		MaxOutputTokens: openai.Int(20),
	}

	// Disable reasoning for title generation to keep it fast and cheap.
	if oc.isOpenRouterProvider() {
		params.Reasoning = shared.ReasoningParam{
			Effort: shared.ReasoningEffortNone,
		}
	}

	// Use Responses API for OpenRouter compatibility (plugins field is only valid here)
	resp, err := oc.api.Responses.New(ctx, params)
	if err != nil && params.Reasoning.Effort != "" {
		oc.loggerForContext(ctx).Warn().Err(err).Str("model", model).Msg("Title generation failed with reasoning disabled; retrying without reasoning param")
		params.Reasoning = shared.ReasoningParam{}
		resp, err = oc.api.Responses.New(ctx, params)
	}
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Str("model", model).Msg("Title generation API call failed")
		return "", err
	}

	title := extractTitleFromResponse(resp)

	if title == "" {
		oc.loggerForContext(ctx).Warn().
			Str("model", model).
			Int("output_items", len(resp.Output)).
			Str("status", string(resp.Status)).
			Msg("Title generation returned no content")
		return "", errors.New("no response from model")
	}

	title = strings.TrimSpace(title)
	title = strings.Trim(title, "\"'")
	if len(title) > 50 {
		title = title[:50]
	}
	return title, nil
}

func extractTitleFromResponse(resp *responses.Response) string {
	var content strings.Builder
	var reasoning strings.Builder

	for _, item := range resp.Output {
		switch item := item.AsAny().(type) {
		case responses.ResponseOutputMessage:
			for _, part := range item.Content {
				// OpenRouter sometimes returns "text" instead of "output_text".
				if part.Type == "output_text" || part.Type == "text" {
					if part.Text != "" {
						content.WriteString(part.Text)
					}
					continue
				}
				if part.Text != "" {
					content.WriteString(part.Text)
				}
				if part.Type == "refusal" && part.Refusal != "" {
					content.WriteString(part.Refusal)
				}
			}
		case responses.ResponseReasoningItem:
			for _, summary := range item.Summary {
				if summary.Text != "" {
					reasoning.WriteString(summary.Text)
				}
			}
		}
	}

	if content.Len() > 0 {
		return content.String()
	}
	if reasoning.Len() > 0 {
		return reasoning.String()
	}
	return ""
}

func (oc *AIClient) getModelContextWindow(meta *PortalMetadata) int {
	responder := oc.responderForMeta(context.Background(), meta)
	if responder != nil && responder.ContextLimit > 0 {
		return responder.ContextLimit
	}
	return 128000
}
