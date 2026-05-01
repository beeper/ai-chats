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

	"github.com/beeper/agentremote/sdk"
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

	turnAccepted := portal != nil && portal.MXID != "" && oc.roomRunAccepted(portal.MXID)
	statusEvents := []*event.Event(nil)
	if !turnAccepted {
		if portal != nil && portal.MXID != "" {
			statusEvents = oc.roomRunStatusEvents(portal.MXID)
		}
		if len(statusEvents) == 0 && evt != nil {
			statusEvents = []*event.Event{evt}
		}
	}
	if len(statusEvents) > 0 {
		msgStatus := bridgev2.WrapErrorInStatus(err).
			WithStatus(messageStatusForError(err)).
			WithErrorReason(messageStatusReasonForError(err)).
			WithMessage(errorMessage).
			WithIsCertain(true).
			WithSendNotice(true)
		for _, statusEvt := range statusEvents {
			sdk.SendMessageStatus(ctx, portal, statusEvt, msgStatus)
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
	if err := oc.updateLoginState(ctx, func(state *loginRuntimeState) bool {
		nextErrors, crossedThreshold = state.RecordProviderError(time.Now(), healthWarningThreshold)
		return true
	}); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to persist provider error state")
		return
	}
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
	if err := oc.updateLoginState(ctx, func(state *loginRuntimeState) bool {
		recovered = state.RecordProviderSuccess(healthWarningThreshold)
		return recovered
	}); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to persist provider recovery state")
		return
	}
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
	sender := oc.senderForPortal(ctx, portal)
	intent, ok := portal.GetIntentFor(ctx, sender, oc.UserLogin, bridgev2.RemoteEventMessage)
	if !ok || intent == nil {
		return
	}
	if typing {
		if err := intent.EnsureJoined(ctx, portal.MXID); err != nil {
			return
		}
	}
	if intent == nil {
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

	bgCtx, cancel := context.WithTimeout(oc.backgroundContext(ctx), 10*time.Second)
	defer cancel()

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
		return fmt.Errorf("send disclaimer: %w", err)
	}
	meta.DisclaimerSent = true
	if err := oc.savePortal(bgCtx, portal, "disclaimer state"); err != nil {
		return fmt.Errorf("persist disclaimer state: %w", err)
	}

	portal.UpdateCapabilities(bgCtx, oc.UserLogin, true)
	return nil
}

func (oc *AIClient) maybeGenerateTitle(ctx context.Context, portal *bridgev2.Portal, userMessageHint, assistantResponse string) {
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

		userMessage := strings.TrimSpace(userMessageHint)
		if userMessage == "" {
			messages, err := oc.getAIHistoryMessages(bgCtx, portal, 10)
			if err != nil {
				oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to get messages for title generation")
				return
			}
			for i := len(messages) - 1; i >= 0; i-- {
				msg := messages[i]
				msgMeta, ok := msg.Metadata.(*MessageMetadata)
				if ok && msgMeta != nil && msgMeta.Role == "user" && msgMeta.Body != "" {
					userMessage = strings.TrimSpace(msgMeta.Body)
					break
				}
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
