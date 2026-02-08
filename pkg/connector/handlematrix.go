package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/agents"
)

type approvalDecisionPayload struct {
	ApprovalID string
	Decision   string
	Reason     string
}

func parseApprovalDecision(raw map[string]any) *approvalDecisionPayload {
	if raw == nil {
		return nil
	}
	payloadRaw, ok := raw["com.beeper.ai.approval_decision"]
	if !ok || payloadRaw == nil {
		return nil
	}
	payloadMap, ok := payloadRaw.(map[string]any)
	if !ok {
		return nil
	}
	approvalID := strings.TrimSpace(readStringArgAny(payloadMap, "approvalId"))
	decision := strings.TrimSpace(readStringArgAny(payloadMap, "decision"))
	reason := strings.TrimSpace(readStringArgAny(payloadMap, "reason"))
	if approvalID == "" || decision == "" {
		return nil
	}
	return &approvalDecisionPayload{
		ApprovalID: approvalID,
		Decision:   decision,
		Reason:     reason,
	}
}

func approvalDecisionFromString(decision string) (approve bool, always bool, ok bool) {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "allow", "approve", "yes", "y", "true", "1", "once":
		return true, false, true
	case "always", "always-allow", "allow-always":
		return true, true, true
	case "deny", "no", "n", "false", "0", "reject":
		return false, false, true
	default:
		return false, false, false
	}
}

func unsupportedMessageStatus(err error) error {
	return bridgev2.WrapErrorInStatus(err).
		WithStatus(event.MessageStatusFail).
		WithErrorReason(event.MessageStatusUnsupported).
		WithIsCertain(true).
		WithSendNotice(true).
		WithErrorAsMessage()
}

func messageSendStatusError(err error, message string, reason event.MessageStatusReason) error {
	if err == nil {
		if message == "" {
			err = errors.New("message send failed")
		} else {
			err = errors.New(message)
		}
	}
	status := bridgev2.WrapErrorInStatus(err).WithSendNotice(true)
	status = status.WithStatus(messageStatusForError(err))
	if reason != "" {
		status = status.WithErrorReason(reason)
	} else {
		status = status.WithErrorReason(messageStatusReasonForError(err))
	}
	if message != "" {
		status = status.WithMessage(message)
	} else {
		status = status.WithErrorAsMessage()
	}
	return status
}

func matrixEventTimestamp(evt *event.Event) time.Time {
	if evt != nil && evt.Timestamp > 0 {
		return time.UnixMilli(evt.Timestamp)
	}
	return time.Now()
}

// HandleMatrixMessage processes incoming Matrix messages and dispatches them to the AI
func (oc *AIClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	if msg.Content == nil {
		return nil, errors.New("missing message content")
	}

	portal := msg.Portal
	if portal == nil {
		return nil, errors.New("portal is nil")
	}
	meta := portalMeta(portal)
	if msg.Event == nil {
		return nil, errors.New("missing message event")
	}
	oc.noteUserActivity(portal.MXID)

	trace := traceEnabled(meta)
	traceFull := traceFull(meta)
	logCtx := oc.loggerForContext(ctx).With().
		Stringer("event_id", msg.Event.ID).
		Stringer("sender", msg.Event.Sender).
		Stringer("portal", portal.PortalKey).
		Logger()
	ctx = logCtx.WithContext(ctx)
	if trace {
		logCtx.Debug().
			Str("msg_type", string(msg.Content.MsgType)).
			Str("event_type", msg.Event.Type.Type).
			Msg("Inbound matrix message received")
	}

	// Track last active room per agent for heartbeat routing
	oc.recordAgentActivity(ctx, portal, meta)

	// Check deduplication - skip if we've already processed this event
	if oc.inboundDedupeCache != nil {
		dedupeKey := oc.buildDedupeKey(portal.MXID, msg.Event.ID)
		if oc.inboundDedupeCache.Check(dedupeKey) {
			logCtx.Debug().Msg("Skipping duplicate message")
			return &bridgev2.MatrixMessageResponse{Pending: false}, nil
		}
	}

	if isMatrixBotUser(ctx, oc.UserLogin.Bridge, msg.Event.Sender) {
		logCtx.Debug().Msg("Ignoring bot message")
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	if decision := parseApprovalDecision(msg.Event.Content.Raw); decision != nil {
		approve, always, ok := approvalDecisionFromString(decision.Decision)
		if !ok {
			logCtx.Warn().Str("decision", decision.Decision).Msg("Unknown approval decision")
			return &bridgev2.MatrixMessageResponse{Pending: false}, nil
		}
		err := oc.resolveToolApproval(portal.MXID, decision.ApprovalID, ToolApprovalDecision{
			Approve:   approve,
			Always:    always,
			Reason:    decision.Reason,
			DecidedAt: time.Now(),
			DecidedBy: msg.Event.Sender,
		})
		if err != nil {
			logCtx.Warn().Err(err).Str("approval_id", decision.ApprovalID).Msg("Failed to resolve approval decision")
			oc.sendToast(ctx, portal, approvalErrorToastText(err), aiToastTypeError)
		}
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	// Route OpenCode rooms to the OpenCode handler (no AI tools or prompt building).
	if meta.IsOpenCodeRoom {
		logCtx.Debug().Msg("Routing message to OpenCode handler")
		if oc.opencodeBridge == nil {
			oc.sendSystemNotice(ctx, portal, "OpenCode integration is not available.")
			return &bridgev2.MatrixMessageResponse{Pending: false}, nil
		}
		return oc.opencodeBridge.HandleMatrixMessage(ctx, msg, portal, oc.PortalMeta(portal))
	}

	// Normalize sticker events to image handling
	msgType := msg.Content.MsgType
	if msg.Event != nil && msg.Event.Type == event.EventSticker {
		msgType = event.MsgImage
	}
	if msgType == event.MessageType(event.EventSticker.Type) {
		msgType = event.MsgImage
	}
	if msgType == event.MsgVideo && msg.Content.Info != nil && msg.Content.Info.MauGIF {
		if mimeType := normalizeMimeType(msg.Content.Info.MimeType); strings.HasPrefix(mimeType, "image/") {
			msgType = event.MsgImage
		}
	}

	// Handle media messages based on type (media is never debounced)
	switch msgType {
	case event.MsgImage, event.MsgVideo, event.MsgAudio, event.MsgFile:
		logCtx.Debug().Str("media_type", string(msgType)).Msg("Handling media message")
		// Flush any pending debounced messages for this room+sender before processing media
		if oc.inboundDebouncer != nil {
			debounceKey := BuildDebounceKey(portal.MXID, msg.Event.Sender)
			oc.inboundDebouncer.FlushKey(debounceKey)
		}
		oc.sendPendingStatus(ctx, portal, msg.Event, "Processing...")
		pendingSent := true
		return oc.handleMediaMessage(ctx, msg, portal, meta, msgType, pendingSent)
	case event.MsgText, event.MsgNotice, event.MsgEmote:
		// Continue to text handling below
	default:
		logCtx.Debug().Str("msg_type", string(msgType)).Msg("Unsupported message type")
		return nil, unsupportedMessageStatus(fmt.Errorf("%s messages are not supported", msgType))
	}
	if msg.Content.RelatesTo != nil && msg.Content.RelatesTo.GetReplaceID() != "" {
		logCtx.Debug().Msg("Ignoring edit event in HandleMatrixMessage")
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	rawBody := strings.TrimSpace(msg.Content.Body)
	if msg.Content.MsgType == event.MsgLocation && strings.TrimSpace(msg.Content.GeoURI) != "" {
		rawMap := msg.Event.Content.Raw
		if loc := resolveMatrixLocation(rawMap); loc != nil && strings.TrimSpace(loc.Text) != "" {
			rawBody = loc.Text
		}
	}
	rawBodyOriginal := rawBody
	if traceFull && rawBodyOriginal != "" {
		logCtx.Debug().Str("body", rawBodyOriginal).Msg("Inbound message body")
	}
	commandAuthorized := oc.isCommandAuthorizedSender(msg.Event.Sender)

	isGroup := oc.isGroupChat(ctx, portal)
	roomName := ""
	if isGroup {
		roomName = oc.matrixRoomDisplayName(ctx, portal)
	}
	senderName := oc.matrixDisplayName(ctx, portal.MXID, msg.Event.Sender)
	logCtx.Debug().
		Bool("is_group", isGroup).
		Bool("command_authorized", commandAuthorized).
		Int("raw_len", len(rawBodyOriginal)).
		Msg("Inbound message metadata resolved")

	var agentDef *agents.AgentDefinition
	if agentID := resolveAgentID(meta); agentID != "" {
		store := NewAgentStoreAdapter(oc)
		if agent, err := store.GetAgentByID(ctx, agentID); err == nil {
			agentDef = agent
		}
	}
	mentionRegexes := buildMentionRegexes(&oc.connector.Config, agentDef)

	commandBody := rawBody
	if isGroup {
		commandBody = stripMentionPatterns(commandBody, mentionRegexes)
	}
	if !commandAuthorized && isControlCommandMessage(commandBody) {
		logCtx.Debug().Msg("Ignoring control command from unauthorized sender")
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	inlineDirs := inlineDirectives{cleaned: rawBody}
	if commandAuthorized {
		inlineDirs = parseInlineDirectives(rawBody)
	}
	rawBody = strings.TrimSpace(inlineDirs.cleaned)

	directiveBody := rawBody
	if isGroup {
		directiveBody = stripMentionPatterns(directiveBody, mentionRegexes)
	}
	directiveOnly := commandAuthorized && inlineDirs.hasAnyDirective() && strings.TrimSpace(directiveBody) == ""
	if inlineDirs.hasAnyDirective() {
		logCtx.Debug().
			Bool("directive_only", directiveOnly).
			Bool("has_status", inlineDirs.hasStatus).
			Msg("Parsed inline directives")
	}

	queueDirective := inlineDirs.queue
	if !commandAuthorized {
		queueDirective = QueueDirective{Cleaned: rawBody}
	}
	inlineMode := QueueMode("")
	inlineOpts := QueueInlineOptions{}
	if !queueDirective.QueueReset {
		if queueDirective.QueueMode != "" {
			inlineMode = queueDirective.QueueMode
		}
		inlineOpts = QueueInlineOptions{
			DebounceMs: queueDirective.DebounceMs,
			Cap:        queueDirective.Cap,
			DropPolicy: queueDirective.DropPolicy,
		}
	}
	queueSettings, _, storeRef, sessionKey := oc.resolveQueueSettingsForPortal(ctx, portal, meta, inlineMode, inlineOpts)
	if commandAuthorized && queueDirective.HasDirective {
		var queueErrors []string
		if queueDirective.HasDebounce && queueDirective.DebounceMs == nil {
			queueErrors = append(queueErrors, fmt.Sprintf(
				"Invalid debounce \"%s\". Use ms/s/m (e.g. debounce:1500ms, debounce:2s).",
				queueDirective.RawDebounce,
			))
		}
		if queueDirective.HasCap && queueDirective.Cap == nil {
			queueErrors = append(queueErrors, fmt.Sprintf(
				"Invalid cap \"%s\". Use a positive integer (e.g. cap:10).",
				queueDirective.RawCap,
			))
		}
		if queueDirective.HasDrop && queueDirective.DropPolicy == nil {
			queueErrors = append(queueErrors, fmt.Sprintf(
				"Invalid drop policy \"%s\". Use drop:old, drop:new, or drop:summarize.",
				queueDirective.RawDrop,
			))
		}
		if len(queueErrors) > 0 {
			oc.sendSystemNotice(ctx, portal, strings.Join(queueErrors, " "))
			if directiveOnly {
				return &bridgev2.MatrixMessageResponse{Pending: false}, nil
			}
		}
	}
	if directiveOnly {
		var responseLines []string
		metaChanged := false

		if inlineDirs.invalidThink {
			oc.sendSystemNotice(ctx, portal, fmt.Sprintf(
				"Unrecognized thinking level \"%s\". Valid: off|minimal|low|medium|high|xhigh.",
				inlineDirs.rawThink,
			))
			return &bridgev2.MatrixMessageResponse{Pending: false}, nil
		}
		if inlineDirs.hasThink {
			if inlineDirs.thinkLevel == "" {
				current := oc.defaultThinkLevel(meta)
				responseLines = append(responseLines, fmt.Sprintf("Thinking: %s", current))
			} else {
				applyThinkingLevel(meta, inlineDirs.thinkLevel)
				metaChanged = true
				responseLines = append(responseLines, formatThinkingAck(inlineDirs.thinkLevel))
			}
		}

		if inlineDirs.invalidVerbose {
			oc.sendSystemNotice(ctx, portal, fmt.Sprintf(
				"Unrecognized verbose level \"%s\". Valid: on|off|full.",
				inlineDirs.rawVerbose,
			))
			return &bridgev2.MatrixMessageResponse{Pending: false}, nil
		}
		if inlineDirs.hasVerbose {
			if inlineDirs.verboseLevel == "" {
				current := meta.VerboseLevel
				if current == "" {
					current = "off"
				}
				responseLines = append(responseLines, fmt.Sprintf("Verbosity: %s", current))
			} else {
				meta.VerboseLevel = inlineDirs.verboseLevel
				metaChanged = true
				responseLines = append(responseLines, formatVerboseAck(inlineDirs.verboseLevel))
			}
		}

		if inlineDirs.invalidReasoning {
			oc.sendSystemNotice(ctx, portal, fmt.Sprintf(
				"Unrecognized reasoning level \"%s\". Valid: off|on|low|medium|high|xhigh.",
				inlineDirs.rawReasoning,
			))
			return &bridgev2.MatrixMessageResponse{Pending: false}, nil
		}
		if inlineDirs.hasReasoning {
			if inlineDirs.reasoningLevel == "" {
				current := meta.ReasoningEffort
				if current == "" {
					if meta.EmitThinking {
						current = "on"
					} else {
						current = "off"
					}
				}
				responseLines = append(responseLines, fmt.Sprintf("Reasoning: %s", current))
			} else {
				applyReasoningLevel(meta, inlineDirs.reasoningLevel)
				metaChanged = true
				responseLines = append(responseLines, formatReasoningAck(inlineDirs.reasoningLevel))
			}
		}

		if inlineDirs.invalidElevated {
			oc.sendSystemNotice(ctx, portal, fmt.Sprintf(
				"Unrecognized elevated level \"%s\". Valid: off|on|ask|full.",
				inlineDirs.rawElevated,
			))
			return &bridgev2.MatrixMessageResponse{Pending: false}, nil
		}
		if inlineDirs.hasElevated {
			if inlineDirs.elevatedLevel == "" {
				current := meta.ElevatedLevel
				if current == "" {
					current = "off"
				}
				responseLines = append(responseLines, fmt.Sprintf("Elevated access: %s", current))
			} else {
				meta.ElevatedLevel = inlineDirs.elevatedLevel
				metaChanged = true
				responseLines = append(responseLines, formatElevatedAck(inlineDirs.elevatedLevel))
			}
		}

		if inlineDirs.hasModel {
			modelAck, modelChanged, modelErr := oc.applyModelDirective(ctx, portal, meta, inlineDirs.rawModel, true)
			if modelErr != "" {
				oc.sendSystemNotice(ctx, portal, modelErr)
				return &bridgev2.MatrixMessageResponse{Pending: false}, nil
			}
			if modelAck != "" {
				responseLines = append(responseLines, modelAck)
			}
			metaChanged = metaChanged || modelChanged
		}

		if queueDirective.HasDirective {
			wantsStatus := queueDirective.QueueMode == "" && !queueDirective.QueueReset && !queueDirective.HasOptions
			if wantsStatus {
				responseLines = append(responseLines, buildQueueStatusLine(queueSettings))
			} else {
				if queueDirective.QueueReset {
					if sessionKey != "" {
						oc.updateSessionEntry(ctx, storeRef, sessionKey, func(entry sessionEntry) sessionEntry {
							entry.QueueMode = ""
							entry.QueueDebounceMs = nil
							entry.QueueCap = nil
							entry.QueueDrop = ""
							entry.UpdatedAt = time.Now().UnixMilli()
							return entry
						})
					}
					oc.clearPendingQueue(portal.MXID)
				} else if sessionKey != "" {
					oc.updateSessionEntry(ctx, storeRef, sessionKey, func(entry sessionEntry) sessionEntry {
						if queueDirective.QueueMode != "" {
							entry.QueueMode = string(queueDirective.QueueMode)
						}
						if queueDirective.DebounceMs != nil {
							entry.QueueDebounceMs = queueDirective.DebounceMs
						}
						if queueDirective.Cap != nil {
							entry.QueueCap = queueDirective.Cap
						}
						if queueDirective.DropPolicy != nil {
							entry.QueueDrop = string(*queueDirective.DropPolicy)
						}
						entry.UpdatedAt = time.Now().UnixMilli()
						return entry
					})
				}
				queueSettings, _, _, _ = oc.resolveQueueSettingsForPortal(ctx, portal, meta, "", QueueInlineOptions{})
				ack := buildQueueDirectiveAck(queueDirective)
				if strings.TrimSpace(ack) == "" {
					ack = "OK."
				}
				responseLines = append(responseLines, ack)
			}
		}

		if metaChanged {
			oc.savePortalQuiet(ctx, portal, "directive change")
		}

		statusText := ""
		if inlineDirs.hasStatus {
			statusText = oc.buildStatusText(ctx, portal, meta, isGroup, queueSettings)
		}
		if statusText != "" {
			responseLines = append(responseLines, statusText)
		}
		if len(responseLines) == 0 && inlineDirs.hasAnyDirective() {
			responseLines = append(responseLines, "OK.")
		}
		if len(responseLines) > 0 {
			oc.sendSystemNotice(ctx, portal, strings.Join(responseLines, "\n"))
		}
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	runMeta := meta
	runCtx := ctx
	var inlineResponses []string
	if commandAuthorized && inlineDirs.hasAnyDirective() {
		if inlineDirs.invalidThink {
			inlineResponses = append(inlineResponses, fmt.Sprintf(
				"Unrecognized thinking level \"%s\". Valid: off|minimal|low|medium|high|xhigh.",
				inlineDirs.rawThink,
			))
		} else if inlineDirs.hasThink && inlineDirs.thinkLevel != "" {
			if runMeta == meta {
				runMeta = clonePortalMetadata(meta)
			}
			applyThinkingLevel(runMeta, inlineDirs.thinkLevel)
			inlineResponses = append(inlineResponses, formatThinkingAck(inlineDirs.thinkLevel))
		}

		if inlineDirs.invalidVerbose {
			inlineResponses = append(inlineResponses, fmt.Sprintf(
				"Unrecognized verbose level \"%s\". Valid: on|off|full.",
				inlineDirs.rawVerbose,
			))
		} else if inlineDirs.hasVerbose && inlineDirs.verboseLevel != "" {
			if runMeta == meta {
				runMeta = clonePortalMetadata(meta)
			}
			runMeta.VerboseLevel = inlineDirs.verboseLevel
			inlineResponses = append(inlineResponses, formatVerboseAck(inlineDirs.verboseLevel))
		}

		if inlineDirs.invalidReasoning {
			inlineResponses = append(inlineResponses, fmt.Sprintf(
				"Unrecognized reasoning level \"%s\". Valid: off|on|low|medium|high|xhigh.",
				inlineDirs.rawReasoning,
			))
		} else if inlineDirs.hasReasoning && inlineDirs.reasoningLevel != "" {
			if runMeta == meta {
				runMeta = clonePortalMetadata(meta)
			}
			applyReasoningLevel(runMeta, inlineDirs.reasoningLevel)
			inlineResponses = append(inlineResponses, formatReasoningAck(inlineDirs.reasoningLevel))
		}

		if inlineDirs.invalidElevated {
			inlineResponses = append(inlineResponses, fmt.Sprintf(
				"Unrecognized elevated level \"%s\". Valid: off|on|ask|full.",
				inlineDirs.rawElevated,
			))
		} else if inlineDirs.hasElevated && inlineDirs.elevatedLevel != "" {
			if runMeta == meta {
				runMeta = clonePortalMetadata(meta)
			}
			runMeta.ElevatedLevel = inlineDirs.elevatedLevel
			inlineResponses = append(inlineResponses, formatElevatedAck(inlineDirs.elevatedLevel))
		}

		if inlineDirs.hasModel && strings.TrimSpace(inlineDirs.rawModel) != "" {
			if runMeta == meta {
				runMeta = clonePortalMetadata(meta)
			}
			ack, modelChanged, modelErr := oc.applyModelDirective(ctx, portal, runMeta, inlineDirs.rawModel, false)
			if modelErr != "" {
				inlineResponses = append(inlineResponses, modelErr)
			} else {
				if ack != "" {
					inlineResponses = append(inlineResponses, ack)
				}
				if modelChanged {
					runCtx = withModelOverride(runCtx, oc.effectiveModel(runMeta))
				}
			}
		}

		if queueDirective.HasDirective {
			wantsStatus := queueDirective.QueueMode == "" && !queueDirective.QueueReset && !queueDirective.HasOptions
			if wantsStatus {
				inlineResponses = append(inlineResponses, buildQueueStatusLine(queueSettings))
			} else if !queueDirective.QueueReset {
				ack := buildQueueDirectiveAck(queueDirective)
				if strings.TrimSpace(ack) == "" {
					ack = "OK."
				}
				inlineResponses = append(inlineResponses, ack)
			}
		}

		if inlineDirs.hasStatus {
			inlineResponses = append(inlineResponses, oc.buildStatusText(ctx, portal, meta, isGroup, queueSettings))
		}
	}

	commandBody = rawBody
	if isGroup {
		commandBody = stripMentionPatterns(commandBody, mentionRegexes)
	}
	if commandAuthorized {
		if cmd, ok := parseInboundCommand(commandBody); ok {
			logCtx.Debug().Str("command", cmd.Name).Bool("has_args", strings.TrimSpace(cmd.Args) != "").Msg("Inbound command parsed")
			switch cmd.Name {
			case "stop", "abort", "interrupt", "exit", "wait", "esc":
				stopped := oc.abortRoom(ctx, portal, meta)
				oc.sendSystemNotice(ctx, portal, formatAbortNotice(stopped))
				logCtx.Debug().Str("command", cmd.Name).Msg("Abort command handled")
				return &bridgev2.MatrixMessageResponse{Pending: false}, nil
			default:
				result := oc.handleInboundCommand(ctx, portal, meta, msg.Event.Sender, isGroup, queueSettings, cmd)
				if result.handled {
					if strings.TrimSpace(result.response) != "" {
						oc.sendSystemNotice(ctx, portal, result.response)
					}
					logCtx.Debug().Str("command", cmd.Name).Msg("Inbound command handled")
					return &bridgev2.MatrixMessageResponse{Pending: false}, nil
				}
				if strings.TrimSpace(result.newBody) != "" {
					rawBody = strings.TrimSpace(result.newBody)
				}
			}
		} else if isAbortTrigger(commandBody) {
			stopped := oc.abortRoom(ctx, portal, meta)
			oc.sendSystemNotice(ctx, portal, formatAbortNotice(stopped))
			logCtx.Debug().Msg("Abort trigger handled")
			return &bridgev2.MatrixMessageResponse{Pending: false}, nil
		}
	}

	if commandAuthorized {
		cleaned := rawBody
		foundHelp := false
		cleaned, foundHelp = extractInlineShortcut(cleaned, []string{"help"})
		foundCommands := false
		if cleaned != "" {
			cleaned, foundCommands = extractInlineShortcut(cleaned, []string{"commands"})
		}
		foundWhoami := false
		if cleaned != "" {
			cleaned, foundWhoami = extractInlineShortcut(cleaned, []string{"whoami", "id"})
		}
		rawBody = strings.TrimSpace(cleaned)
		if foundHelp || foundCommands {
			helpText := "Commands: /status, /context, /tools, /cron, /typing, /model, /think, /verbose, /reasoning, /elevated, /activation, /send, /queue, /new, /reset, /stop"
			inlineResponses = append(inlineResponses, helpText)
		}
		if foundWhoami {
			inlineResponses = append(inlineResponses, fmt.Sprintf("You are %s.", msg.Event.Sender.String()))
		}
	}

	if len(inlineResponses) > 0 {
		oc.sendSystemNotice(ctx, portal, strings.Join(inlineResponses, "\n"))
	}

	if rawBody == "" {
		if len(inlineResponses) > 0 {
			return &bridgev2.MatrixMessageResponse{Pending: false}, nil
		}
		return nil, unsupportedMessageStatus(errors.New("empty messages are not supported"))
	}

	// Mention detection (OpenClaw-style)
	replyCtx := extractInboundReplyContext(msg.Event)
	botMXID := oc.resolveBotMXID(ctx, portal, meta)
	explicitMention := false
	hasExplicit := false
	if msg.Content.Mentions != nil {
		hasExplicit = true
		if msg.Content.Mentions.Room || (botMXID != "" && msg.Content.Mentions.Has(botMXID)) {
			explicitMention = true
		}
	}
	if !explicitMention && replyCtx.ReplyTo != "" {
		if oc.isReplyToBot(ctx, portal, replyCtx.ReplyTo) {
			explicitMention = true
		}
	}
	wasMentioned := explicitMention || matchesMentionPatterns(rawBody, mentionRegexes)
	groupActivation := oc.resolveGroupActivation(meta)
	requireMention := isGroup && groupActivation != "always"
	canDetectMention := len(mentionRegexes) > 0 || hasExplicit
	shouldBypassMention := groupActivation == "always"
	if isGroup && requireMention && !wasMentioned && !shouldBypassMention {
		logCtx.Debug().
			Bool("require_mention", requireMention).
			Bool("was_mentioned", wasMentioned).
			Str("activation", groupActivation).
			Msg("Ignoring group message without mention")
		historyLimit := oc.resolveGroupHistoryLimit()
		if historyLimit > 0 {
			historyBody := oc.buildMatrixInboundBody(ctx, portal, meta, msg.Event, rawBodyOriginal, senderName, roomName, isGroup)
			oc.recordPendingGroupHistory(portal.MXID, historyBody, historyLimit)
		}
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	pendingSent := false

	// Ack reaction (OpenClaw-style scope gating)
	ackReaction := strings.TrimSpace(meta.AckReactionEmoji)
	if ackReaction == "" && oc.connector != nil && oc.connector.Config.Messages != nil {
		ackReaction = strings.TrimSpace(oc.connector.Config.Messages.AckReaction)
	}
	ackScope := AckScopeGroupMention
	if oc.connector != nil && oc.connector.Config.Messages != nil {
		ackScope = normalizeAckScope(oc.connector.Config.Messages.AckReactionScope)
	}
	removeAckAfter := meta.AckReactionRemoveAfter
	if !removeAckAfter && oc.connector != nil && oc.connector.Config.Messages != nil && oc.connector.Config.Messages.RemoveAckAfter {
		removeAckAfter = true
	}
	meta.AckReactionRemoveAfter = removeAckAfter

	var ackReactionEventID id.EventID
	if ackReaction != "" && shouldAckReaction(AckReactionGateParams{
		Scope:              ackScope,
		IsDirect:           !isGroup,
		IsGroup:            isGroup,
		IsMentionableGroup: isGroup,
		RequireMention:     requireMention,
		CanDetectMention:   canDetectMention,
		EffectiveMention:   wasMentioned || shouldBypassMention,
		ShouldBypass:       shouldBypassMention,
	}) {
		ackReactionEventID = oc.sendAckReaction(ctx, portal, msg.Event.ID, ackReaction)
	}
	if ackReactionEventID != "" && removeAckAfter {
		oc.storeAckReaction(portal.MXID, msg.Event.ID, ackReactionEventID)
	}
	if trace {
		logCtx.Debug().
			Str("ack_reaction", ackReaction).
			Bool("sent", ackReactionEventID != "").
			Bool("remove_after", removeAckAfter).
			Msg("Ack reaction evaluated")
	}

	body := oc.buildMatrixInboundBody(ctx, portal, meta, msg.Event, rawBody, senderName, roomName, isGroup)
	if isGroup && requireMention {
		body = oc.buildGroupHistoryContext(portal.MXID, body, oc.resolveGroupHistoryLimit())
	}

	// Check if this message should be debounced
	debounceDelay := meta.DebounceMs
	if debounceDelay == 0 {
		debounceDelay = oc.resolveInboundDebounceMs("matrix")
	}
	shouldDebounce := oc.inboundDebouncer != nil && ShouldDebounce(msg.Event, rawBody) && debounceDelay > 0
	debounceKey := ""
	if oc.inboundDebouncer != nil {
		debounceKey = BuildDebounceKey(portal.MXID, msg.Event.Sender)
	}

	if shouldDebounce {
		logCtx.Debug().Int("debounce_ms", debounceDelay).Msg("Debouncing inbound message")
		entry := DebounceEntry{
			Event:        msg.Event,
			Portal:       portal,
			Meta:         runMeta,
			RawBody:      rawBody,
			SenderName:   senderName,
			RoomName:     roomName,
			IsGroup:      isGroup,
			WasMentioned: wasMentioned,
			AckEventID:   ackReactionEventID,
			PendingSent:  pendingSent,
		}
		// Let the client know the message is pending due to debounce.
		if debounceDelay >= 0 && !pendingSent {
			oc.sendPendingStatus(ctx, portal, msg.Event, "Combining messages...")
			entry.PendingSent = true
		}
		oc.inboundDebouncer.EnqueueWithDelay(debounceKey, entry, true, debounceDelay)
		return &bridgev2.MatrixMessageResponse{Pending: true}, nil
	}
	if debounceKey != "" {
		// Flush any pending debounced messages for this room+sender before immediate processing
		oc.inboundDebouncer.FlushKey(debounceKey)
	}

	// Not debouncing - process immediately
	// Get raw event content for link previews
	var rawEventContent map[string]any
	if msg.Event != nil && msg.Event.Content.Raw != nil {
		rawEventContent = msg.Event.Content.Raw
	}

	eventID := id.EventID("")
	if msg.Event != nil {
		eventID = msg.Event.ID
	}

	promptMessages, err := oc.buildPromptWithLinkContext(runCtx, portal, runMeta, body, rawEventContent, eventID)
	if err != nil {
		return nil, messageSendStatusError(err, "Couldn't prepare the message. Try again.", "")
	}
	logCtx.Debug().Int("prompt_messages", len(promptMessages)).Msg("Built prompt for inbound message")
	userMessage := &database.Message{
		ID:       networkid.MessageID(fmt.Sprintf("mx:%s", string(eventID))),
		MXID:     eventID,
		Room:     portal.PortalKey,
		SenderID: humanUserID(oc.UserLogin.ID),
		Metadata: &MessageMetadata{
			Role: "user",
			Body: body,
		},
		Timestamp: matrixEventTimestamp(msg.Event),
	}
	if msg.InputTransactionID != "" {
		userMessage.SendTxnID = networkid.RawTransactionID(msg.InputTransactionID)
	}

	pending := pendingMessage{
		Event:           msg.Event,
		Portal:          portal,
		Meta:            runMeta,
		Type:            pendingTypeText,
		MessageBody:     body,
		RawEventContent: rawEventContent,
		AckEventIDs:     []id.EventID{msg.Event.ID},
		PendingSent:     pendingSent,
		Typing: &TypingContext{
			IsGroup:      isGroup,
			WasMentioned: wasMentioned,
		},
	}
	queueItem := pendingQueueItem{
		pending:         pending,
		messageID:       string(eventID),
		summaryLine:     rawBodyOriginal,
		enqueuedAt:      time.Now().UnixMilli(),
		rawEventContent: rawEventContent,
	}
	dbMsg, isPending := oc.dispatchOrQueue(runCtx, msg.Event, portal, runMeta, userMessage, queueItem, queueSettings, promptMessages)

	return &bridgev2.MatrixMessageResponse{
		DB:      dbMsg,
		Pending: isPending,
	}, nil
}

// HandleMatrixTyping tracks local user typing state for auto-greeting delays.
func (oc *AIClient) HandleMatrixTyping(ctx context.Context, typing *bridgev2.MatrixTyping) error {
	if typing == nil || typing.Portal == nil {
		return nil
	}
	if typing.Portal.MXID == "" {
		return nil
	}
	oc.setUserTyping(typing.Portal.MXID, typing.IsTyping)
	return nil
}

// HandleMatrixEdit handles edits to previously sent messages
func (oc *AIClient) HandleMatrixEdit(ctx context.Context, edit *bridgev2.MatrixEdit) error {
	if edit.Content == nil || edit.EditTarget == nil {
		return errors.New("invalid edit: missing content or target")
	}

	portal := edit.Portal
	if portal == nil {
		return errors.New("portal is nil")
	}
	meta := portalMeta(portal)
	trace := traceEnabled(meta)
	traceFull := traceFull(meta)
	logCtx := zerolog.Nop()
	if trace {
		logCtx = oc.loggerForContext(ctx).With().
			Stringer("portal", portal.PortalKey).
			Logger()
		if edit.Event != nil {
			logCtx = logCtx.With().Stringer("event_id", edit.Event.ID).Logger()
		}
		logCtx.Debug().Msg("Inbound edit received")
	}
	if meta != nil && meta.IsOpenCodeRoom {
		logCtx.Debug().Msg("Edit ignored for OpenCode room")
		return errors.New("editing is not supported for OpenCode rooms")
	}

	// Get the new message body
	newBody := strings.TrimSpace(edit.Content.Body)
	if newBody == "" {
		logCtx.Debug().Msg("Edit body is empty")
		return errors.New("empty edit body")
	}
	if traceFull {
		logCtx.Debug().Str("body", newBody).Msg("Edited message body")
	}

	// Update the message metadata with the new content
	msgMeta := messageMeta(edit.EditTarget)
	if msgMeta == nil {
		msgMeta = &MessageMetadata{}
		edit.EditTarget.Metadata = msgMeta
	}
	msgMeta.Body = newBody

	// Persist the updated metadata
	if err := oc.UserLogin.Bridge.DB.Message.Update(ctx, edit.EditTarget); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to persist edited message metadata")
	}
	oc.notifySessionMemoryChange(ctx, portal, meta, true)

	// Only regenerate if this was a user message
	if msgMeta.Role != "user" {
		// Just update the content, don't regenerate
		logCtx.Debug().Str("role", msgMeta.Role).Msg("Edit did not target user message; skipping regeneration")
		return nil
	}

	oc.loggerForContext(ctx).Info().
		Str("message_id", string(edit.EditTarget.ID)).
		Int("new_body_len", len(newBody)).
		Msg("User edited message, regenerating response")

	// Find the assistant response that came after this message
	// We'll delete it and regenerate
	err := oc.regenerateFromEdit(ctx, edit.Event, portal, meta, edit.EditTarget, newBody)
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
	messages, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, 50)
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

	// Build the prompt with the edited message included
	// We need to rebuild from scratch up to the edited message
	promptMessages, err := oc.buildPromptUpToMessage(ctx, portal, meta, editedMessage.ID, newBody)
	if err != nil {
		return fmt.Errorf("failed to build prompt: %w", err)
	}

	// If we found an assistant response, we'll redact/edit it
	if assistantResponse != nil {
		// Try to redact the old response
		if assistantResponse.MXID != "" {
			intent, _ := portal.GetIntentFor(ctx, bridgev2.EventSender{IsFromMe: true}, oc.UserLogin, bridgev2.RemoteEventMessageRemove)
			if intent != nil {
				_, _ = intent.SendMessage(ctx, portal.MXID, event.EventRedaction, &event.Content{
					Parsed: &event.RedactionEventContent{
						Redacts: assistantResponse.MXID,
					},
				}, nil)
			}
		}
		// Clean up database record to prevent orphaned messages
		if err := oc.UserLogin.Bridge.DB.Message.Delete(ctx, assistantResponse.RowID); err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Str("msg_id", string(assistantResponse.ID)).Msg("Failed to delete redacted message from database")
		}
		oc.notifySessionMemoryChange(ctx, portal, meta, true)
	}

	queueSettings, _, _, _ := oc.resolveQueueSettingsForPortal(ctx, portal, meta, "", QueueInlineOptions{})
	isGroup := oc.isGroupChat(ctx, portal)
	pending := pendingMessage{
		Event:       evt,
		Portal:      portal,
		Meta:        meta,
		Type:        pendingTypeEditRegenerate,
		MessageBody: newBody,
		TargetMsgID: editedMessage.ID,
		Typing: &TypingContext{
			IsGroup:      isGroup,
			WasMentioned: true,
		},
	}
	queueItem := pendingQueueItem{
		pending:     pending,
		messageID:   string(evt.ID),
		summaryLine: newBody,
		enqueuedAt:  time.Now().UnixMilli(),
	}
	oc.dispatchOrQueueWithStatus(ctx, evt, portal, meta, queueItem, queueSettings, promptMessages)

	return nil
}

// mediaConfig describes how to handle a specific media type
type mediaConfig struct {
	msgType         pendingMessageType
	capabilityCheck func(*ModelCapabilities) bool
	capabilityName  string
	defaultCaption  string
	bodySuffix      string
	defaultMimeType string
}

var mediaConfigs = map[event.MessageType]mediaConfig{
	event.MsgImage: {
		msgType:         pendingTypeImage,
		capabilityCheck: func(c *ModelCapabilities) bool { return c.SupportsVision },
		capabilityName:  "image analysis",
		defaultCaption:  "What's in this image?",
		bodySuffix:      " [image]",
	},
	event.MsgAudio: {
		msgType:         pendingTypeAudio,
		capabilityCheck: func(c *ModelCapabilities) bool { return c.SupportsAudio },
		capabilityName:  "audio input",
		defaultCaption:  "Please transcribe or analyze this audio.",
		bodySuffix:      " [audio]",
		defaultMimeType: "audio/mpeg",
	},
	event.MsgVideo: {
		msgType:         pendingTypeVideo,
		capabilityCheck: func(c *ModelCapabilities) bool { return c.SupportsVideo },
		capabilityName:  "video input",
		defaultCaption:  "Please analyze this video.",
		bodySuffix:      " [video]",
	},
}

// pdfConfig is handled separately due to special OpenRouter capability check
var pdfConfig = mediaConfig{
	msgType:         pendingTypePDF,
	capabilityCheck: func(c *ModelCapabilities) bool { return c.SupportsPDF },
	capabilityName:  "PDF analysis",
	defaultCaption:  "Please analyze this PDF document.",
	bodySuffix:      " [PDF]",
	defaultMimeType: "application/pdf",
}

// handleMediaMessage processes media messages (image, PDF, audio, video)
func (oc *AIClient) handleMediaMessage(
	ctx context.Context,
	msg *bridgev2.MatrixMessage,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	msgType event.MessageType,
	pendingSent bool,
) (*bridgev2.MatrixMessageResponse, error) {
	trace := traceEnabled(meta)
	traceFull := traceFull(meta)
	logCtx := zerolog.Nop()
	if trace {
		logCtx = oc.loggerForContext(ctx).With().
			Stringer("event_id", msg.Event.ID).
			Stringer("portal", portal.PortalKey).
			Logger()
		logCtx.Debug().Str("msg_type", string(msgType)).Msg("Handling media message")
	}
	isGroup := oc.isGroupChat(ctx, portal)
	roomName := ""
	if isGroup {
		roomName = oc.matrixRoomDisplayName(ctx, portal)
	}
	senderName := oc.matrixDisplayName(ctx, portal.MXID, msg.Event.Sender)

	// Get config for this media type
	config, ok := mediaConfigs[msgType]
	isPDF := false

	// Get the media URL
	mediaURL := msg.Content.URL
	if mediaURL == "" && msg.Content.File != nil {
		mediaURL = msg.Content.File.URL
	}
	if mediaURL == "" {
		return nil, unsupportedMessageStatus(fmt.Errorf("%s message has no URL", msgType))
	}

	// Get MIME type
	mimeType := ""
	if msg.Content.Info != nil && msg.Content.Info.MimeType != "" {
		mimeType = normalizeMimeType(msg.Content.Info.MimeType)
	}

	// Handle PDF or text files (MsgFile)
	if msgType == event.MsgFile {
		switch {
		case mimeType == "application/pdf":
			config = pdfConfig
			isPDF = true
			ok = true
		case isTextFileMime(mimeType):
			if !oc.canUseMediaUnderstanding(meta) {
				return nil, unsupportedMessageStatus(errors.New("text file understanding is only available when an agent is assigned and raw mode is off"))
			}
			return oc.handleTextFileMessage(ctx, msg, portal, meta, string(mediaURL), mimeType, pendingSent)
		case mimeType == "" || mimeType == "application/octet-stream":
			if !oc.canUseMediaUnderstanding(meta) {
				return nil, unsupportedMessageStatus(errors.New("text file understanding is only available when an agent is assigned and raw mode is off"))
			}
			return oc.handleTextFileMessage(ctx, msg, portal, meta, string(mediaURL), mimeType, pendingSent)
		}
	}

	if !ok {
		logCtx.Debug().Str("msg_type", string(msgType)).Msg("Unsupported media type")
		return nil, unsupportedMessageStatus(fmt.Errorf("unsupported media type: %s", msgType))
	}

	if mimeType == "" {
		mimeType = config.defaultMimeType
	}
	if trace {
		logCtx.Debug().
			Str("mime_type", mimeType).
			Bool("is_pdf", isPDF).
			Str("capability", config.capabilityName).
			Msg("Resolved media metadata")
	}
	if traceFull {
		caption := strings.TrimSpace(msg.Content.Body)
		if caption != "" {
			logCtx.Debug().Str("caption", caption).Msg("Media caption")
		}
	}

	eventID := id.EventID("")
	if msg.Event != nil {
		eventID = msg.Event.ID
	}

	// Check capability (PDF has special OpenRouter handling via file-parser plugin)
	modelCaps := oc.getModelCapabilitiesForMeta(meta)
	supportsMedia := config.capabilityCheck(&modelCaps)
	if isPDF && !supportsMedia && oc.isOpenRouterProvider() {
		supportsMedia = true // OpenRouter supports PDF via file-parser plugin
	}
	if trace {
		logCtx.Debug().Bool("supports_media", supportsMedia).Msg("Media capability check")
	}
	queueSettings, _, _, _ := oc.resolveQueueSettingsForPortal(ctx, portal, meta, "", QueueInlineOptions{})

	// Get caption (body is usually the filename or caption)
	rawCaption := strings.TrimSpace(msg.Content.Body)
	hasUserCaption := rawCaption != ""
	if msg.Content.Info != nil && rawCaption == msg.Content.Info.MimeType {
		hasUserCaption = false
	}
	caption := rawCaption
	if !hasUserCaption {
		caption = config.defaultCaption
	}

	agentDef := (*agents.AgentDefinition)(nil)
	if agentID := resolveAgentID(meta); agentID != "" {
		store := NewAgentStoreAdapter(oc)
		if agent, err := store.GetAgentByID(ctx, agentID); err == nil {
			agentDef = agent
		}
	}
	mentionRegexes := buildMentionRegexes(&oc.connector.Config, agentDef)
	replyCtx := extractInboundReplyContext(msg.Event)
	botMXID := oc.resolveBotMXID(ctx, portal, meta)
	explicitMention := false
	if msg.Content.Mentions != nil {
		if msg.Content.Mentions.Room || (botMXID != "" && msg.Content.Mentions.Has(botMXID)) {
			explicitMention = true
		}
	}
	if !explicitMention && replyCtx.ReplyTo != "" {
		if oc.isReplyToBot(ctx, portal, replyCtx.ReplyTo) {
			explicitMention = true
		}
	}
	wasMentioned := explicitMention || matchesMentionPatterns(rawCaption, mentionRegexes)
	typingCtx := &TypingContext{IsGroup: isGroup, WasMentioned: wasMentioned}

	// Get encrypted file info if present (for E2EE rooms)
	var encryptedFile *event.EncryptedFileInfo
	if msg.Content.File != nil {
		encryptedFile = msg.Content.File
	}

	dispatchTextOnly := func(rawBody string) (*bridgev2.MatrixMessageResponse, error) {
		body := oc.buildMatrixInboundBody(ctx, portal, meta, msg.Event, rawBody, senderName, roomName, isGroup)
		promptMessages, err := oc.buildPrompt(ctx, portal, meta, body, eventID)
		if err != nil {
			return nil, messageSendStatusError(err, "Couldn't prepare the message. Try again.", "")
		}
		userMessage := &database.Message{
			ID:       networkid.MessageID(fmt.Sprintf("mx:%s", string(eventID))),
			MXID:     eventID,
			Room:     portal.PortalKey,
			SenderID: humanUserID(oc.UserLogin.ID),
			Metadata: &MessageMetadata{
				Role: "user",
				Body: body,
			},
			Timestamp: matrixEventTimestamp(msg.Event),
		}
		if msg.InputTransactionID != "" {
			userMessage.SendTxnID = networkid.RawTransactionID(msg.InputTransactionID)
		}
		pending := pendingMessage{
			Event:       msg.Event,
			Portal:      portal,
			Meta:        meta,
			Type:        pendingTypeText,
			MessageBody: body,
			PendingSent: pendingSent,
			Typing:      typingCtx,
		}
		queueItem := pendingQueueItem{
			pending:     pending,
			messageID:   string(eventID),
			summaryLine: rawBody,
			enqueuedAt:  time.Now().UnixMilli(),
		}
		dbMsg, isPending := oc.dispatchOrQueue(ctx, msg.Event, portal, meta, userMessage, queueItem, queueSettings, promptMessages)
		return &bridgev2.MatrixMessageResponse{
			DB:      dbMsg,
			Pending: isPending,
		}, nil
	}

	var understanding *mediaUnderstandingResult
	if capability, ok := mediaCapabilityForMessage(msgType); ok {
		attachments := []mediaAttachment{{
			Index:         0,
			URL:           string(mediaURL),
			MimeType:      mimeType,
			EncryptedFile: encryptedFile,
			FileName:      strings.TrimSpace(msg.Content.FileName),
		}}
		if result, err := oc.applyMediaUnderstandingForAttachments(ctx, portal, meta, capability, attachments, rawCaption, hasUserCaption); err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Msg("Media understanding failed")
		} else if result != nil {
			understanding = result
			if strings.TrimSpace(result.Body) != "" {
				caption = result.Body
			}
		}
	}

	if !supportsMedia {
		if understanding != nil && strings.TrimSpace(understanding.Body) != "" {
			return dispatchTextOnly(understanding.Body)
		}

		// If model lacks vision but agent supports image understanding, analyze image first.
		if msgType == event.MsgImage {
			visionModel, visionFallback := oc.resolveVisionModelForImage(ctx, meta)
			if visionFallback && visionModel != "" {
				analysisPrompt := buildImageUnderstandingPrompt(caption, hasUserCaption)
				description, err := oc.analyzeImageWithModel(ctx, visionModel, string(mediaURL), mimeType, encryptedFile, analysisPrompt)
				if err != nil {
					oc.loggerForContext(ctx).Warn().Err(err).Msg("Image understanding failed")
					return nil, messageSendStatusError(err, "Couldn't analyze the image. Try again, or switch to a vision-capable model with /model.", "")
				}

				combined := buildImageUnderstandingMessage(caption, hasUserCaption, description)
				if combined == "" {
					return nil, messageSendStatusError(errors.New("image understanding produced empty result"), "Couldn't analyze the image. Try again, or switch to a vision-capable model with /model.", "")
				}
				return dispatchTextOnly(combined)
			}
		}

		// If model lacks audio but agent supports audio understanding, analyze audio first.
		if msgType == event.MsgAudio {
			audioModel, audioFallback := oc.resolveAudioModelForInput(ctx, meta)
			if audioFallback && audioModel != "" {
				analysisPrompt := buildAudioUnderstandingPrompt(caption, hasUserCaption)
				transcript, err := oc.analyzeAudioWithModel(ctx, audioModel, string(mediaURL), mimeType, encryptedFile, analysisPrompt)
				if err != nil {
					oc.loggerForContext(ctx).Warn().Err(err).Msg("Audio understanding failed")
					return nil, messageSendStatusError(err, "Couldn't analyze the audio. Try again, or switch to an audio-capable model with /model.", "")
				}

				combined := buildAudioUnderstandingMessage(caption, hasUserCaption, transcript)
				if combined == "" {
					return nil, messageSendStatusError(errors.New("audio understanding produced empty result"), "Couldn't analyze the audio. Try again, or switch to an audio-capable model with /model.", "")
				}
				return dispatchTextOnly(combined)
			}
		}

		return nil, unsupportedMessageStatus(fmt.Errorf(
			"current model (%s) does not support %s; switch to a capable model using /model",
			oc.effectiveModel(meta), config.capabilityName,
		))
	}

	// Build prompt with media
	captionForPrompt := oc.buildMatrixInboundBody(ctx, portal, meta, msg.Event, caption, senderName, roomName, isGroup)
	promptMessages, err := oc.buildPromptWithMedia(ctx, portal, meta, captionForPrompt, string(mediaURL), mimeType, encryptedFile, config.msgType, eventID)
	if err != nil {
		return nil, messageSendStatusError(err, "Couldn't prepare the media message. Try again.", "")
	}

	userMeta := &MessageMetadata{
		Role: "user",
		Body: oc.buildMatrixInboundBody(ctx, portal, meta, msg.Event, buildMediaMetadataBody(caption, config.bodySuffix, understanding), senderName, roomName, isGroup),
	}
	if understanding != nil {
		userMeta.MediaUnderstanding = understanding.Outputs
		userMeta.MediaUnderstandingDecisions = understanding.Decisions
		userMeta.Transcript = understanding.Transcript
	}

	userMessage := &database.Message{
		ID:        networkid.MessageID(fmt.Sprintf("mx:%s", string(eventID))),
		MXID:      eventID,
		Room:      portal.PortalKey,
		SenderID:  humanUserID(oc.UserLogin.ID),
		Metadata:  userMeta,
		Timestamp: matrixEventTimestamp(msg.Event),
	}
	if msg.InputTransactionID != "" {
		userMessage.SendTxnID = networkid.RawTransactionID(msg.InputTransactionID)
	}

	pending := pendingMessage{
		Event:         msg.Event,
		Portal:        portal,
		Meta:          meta,
		Type:          config.msgType,
		MessageBody:   captionForPrompt,
		MediaURL:      string(mediaURL),
		MimeType:      mimeType,
		EncryptedFile: encryptedFile,
		PendingSent:   pendingSent,
		Typing:        typingCtx,
	}
	queueItem := pendingQueueItem{
		pending:     pending,
		messageID:   string(eventID),
		summaryLine: rawCaption,
		enqueuedAt:  time.Now().UnixMilli(),
	}
	dbMsg, isPending := oc.dispatchOrQueue(ctx, msg.Event, portal, meta, userMessage, queueItem, queueSettings, promptMessages)

	return &bridgev2.MatrixMessageResponse{
		DB:      dbMsg,
		Pending: isPending,
	}, nil
}

func (oc *AIClient) handleTextFileMessage(
	ctx context.Context,
	msg *bridgev2.MatrixMessage,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	mediaURL string,
	mimeType string,
	pendingSent bool,
) (*bridgev2.MatrixMessageResponse, error) {
	if msg == nil {
		return nil, errors.New("missing matrix event for text file message")
	}
	queueSettings, _, _, _ := oc.resolveQueueSettingsForPortal(ctx, portal, meta, "", QueueInlineOptions{})

	rawCaption := strings.TrimSpace(msg.Content.Body)
	fileName := strings.TrimSpace(msg.Content.FileName)
	hasUserCaption := rawCaption != ""
	if fileName == "" {
		fileName = rawCaption
		hasUserCaption = false
	}
	if rawCaption == fileName {
		hasUserCaption = false
	}
	caption := rawCaption
	if !hasUserCaption {
		caption = "Please analyze this text file."
	}

	isGroup := oc.isGroupChat(ctx, portal)
	agentDef := (*agents.AgentDefinition)(nil)
	if agentID := resolveAgentID(meta); agentID != "" {
		store := NewAgentStoreAdapter(oc)
		if agent, err := store.GetAgentByID(ctx, agentID); err == nil {
			agentDef = agent
		}
	}
	mentionRegexes := buildMentionRegexes(&oc.connector.Config, agentDef)
	replyCtx := extractInboundReplyContext(msg.Event)
	botMXID := oc.resolveBotMXID(ctx, portal, meta)
	explicitMention := false
	if msg.Content.Mentions != nil {
		if msg.Content.Mentions.Room || (botMXID != "" && msg.Content.Mentions.Has(botMXID)) {
			explicitMention = true
		}
	}
	if !explicitMention && replyCtx.ReplyTo != "" {
		if oc.isReplyToBot(ctx, portal, replyCtx.ReplyTo) {
			explicitMention = true
		}
	}
	wasMentioned := explicitMention || matchesMentionPatterns(rawCaption, mentionRegexes)
	typingCtx := &TypingContext{IsGroup: isGroup, WasMentioned: wasMentioned}

	var encryptedFile *event.EncryptedFileInfo
	if msg.Content.File != nil {
		encryptedFile = msg.Content.File
	}

	content, truncated, err := oc.downloadTextFile(ctx, mediaURL, encryptedFile, mimeType)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Text file understanding failed")
		return nil, messageSendStatusError(err, "Couldn't read the text file. Upload a UTF-8 text file under 5 MB.", "")
	}

	combined := buildTextFileMessage(caption, hasUserCaption, fileName, mimeType, content, truncated)
	if combined == "" {
		return nil, messageSendStatusError(errors.New("text file understanding produced empty result"), "Couldn't read the text file. Upload a UTF-8 text file under 5 MB.", "")
	}

	eventID := id.EventID("")
	if msg.Event != nil {
		eventID = msg.Event.ID
	}

	promptMessages, err := oc.buildPrompt(ctx, portal, meta, combined, eventID)
	if err != nil {
		return nil, messageSendStatusError(err, "Couldn't prepare the message. Try again.", "")
	}

	userMessage := &database.Message{
		ID:       networkid.MessageID(fmt.Sprintf("mx:%s", string(eventID))),
		MXID:     eventID,
		Room:     portal.PortalKey,
		SenderID: humanUserID(oc.UserLogin.ID),
		Metadata: &MessageMetadata{
			Role: "user",
			Body: combined,
		},
		Timestamp: matrixEventTimestamp(msg.Event),
	}
	if msg.InputTransactionID != "" {
		userMessage.SendTxnID = networkid.RawTransactionID(msg.InputTransactionID)
	}

	pending := pendingMessage{
		Event:       msg.Event,
		Portal:      portal,
		Meta:        meta,
		Type:        pendingTypeText,
		MessageBody: combined,
		PendingSent: pendingSent,
		Typing:      typingCtx,
	}
	queueItem := pendingQueueItem{
		pending:     pending,
		messageID:   string(eventID),
		summaryLine: strings.TrimSpace(rawCaption),
		enqueuedAt:  time.Now().UnixMilli(),
	}
	dbMsg, isPending := oc.dispatchOrQueue(ctx, msg.Event, portal, meta, userMessage, queueItem, queueSettings, promptMessages)

	return &bridgev2.MatrixMessageResponse{
		DB:      dbMsg,
		Pending: isPending,
	}, nil
}

// savePortalQuiet saves portal and logs errors without failing
func (oc *AIClient) savePortalQuiet(ctx context.Context, portal *bridgev2.Portal, action string) {
	if err := portal.Save(ctx); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Str("action", action).Msg("Failed to save portal")
	}
}

// Ack reaction tracking for removal after reply
// Maps room ID -> source message ID -> ack reaction event ID
const (
	ackReactionTTL             = 5 * time.Minute
	ackReactionCleanupInterval = time.Minute
)

type ackReactionEntry struct {
	reactionEventID id.EventID
	storedAt        time.Time
}

var (
	ackReactionStore   = make(map[id.RoomID]map[id.EventID]ackReactionEntry)
	ackReactionStoreMu sync.Mutex
)

func init() {
	go cleanupAckReactionStore()
}

func cleanupAckReactionStore() {
	ticker := time.NewTicker(ackReactionCleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		cutoff := time.Now().Add(-ackReactionTTL)
		ackReactionStoreMu.Lock()
		for roomID, roomReactions := range ackReactionStore {
			for sourceEventID, entry := range roomReactions {
				if entry.storedAt.Before(cutoff) {
					delete(roomReactions, sourceEventID)
				}
			}
			if len(roomReactions) == 0 {
				delete(ackReactionStore, roomID)
			}
		}
		ackReactionStoreMu.Unlock()
	}
}

// sendAckReaction sends an acknowledgement reaction to a message.
// Returns the event ID of the reaction for potential removal.
func (oc *AIClient) sendAckReaction(ctx context.Context, portal *bridgev2.Portal, targetEventID id.EventID, emoji string) id.EventID {
	if portal == nil || portal.MXID == "" || targetEventID == "" || emoji == "" {
		return ""
	}
	if err := oc.ensureModelInRoom(ctx, portal); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to ensure ghost is in room for ack reaction")
		return ""
	}
	intent := oc.getModelIntent(ctx, portal)
	if intent == nil {
		return ""
	}

	eventContent := &event.Content{
		Raw: map[string]any{
			"m.relates_to": map[string]any{
				"rel_type": "m.annotation",
				"event_id": targetEventID.String(),
				"key":      emoji,
			},
		},
	}

	resp, err := intent.SendMessage(ctx, portal.MXID, event.EventReaction, eventContent, nil)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).
			Stringer("target_event", targetEventID).
			Str("emoji", emoji).
			Msg("Failed to send ack reaction")
		return ""
	}

	oc.loggerForContext(ctx).Debug().
		Stringer("target_event", targetEventID).
		Str("emoji", emoji).
		Stringer("reaction_event", resp.EventID).
		Msg("Sent ack reaction")
	return resp.EventID
}

// storeAckReaction stores an ack reaction for later removal.
func (oc *AIClient) storeAckReaction(roomID id.RoomID, sourceEventID, reactionEventID id.EventID) {
	ackReactionStoreMu.Lock()
	defer ackReactionStoreMu.Unlock()

	if ackReactionStore[roomID] == nil {
		ackReactionStore[roomID] = make(map[id.EventID]ackReactionEntry)
	}
	ackReactionStore[roomID][sourceEventID] = ackReactionEntry{
		reactionEventID: reactionEventID,
		storedAt:        time.Now(),
	}
}

// removeAckReaction removes a previously sent ack reaction.
func (oc *AIClient) removeAckReaction(ctx context.Context, portal *bridgev2.Portal, sourceEventID id.EventID) {
	ackReactionStoreMu.Lock()
	roomReactions := ackReactionStore[portal.MXID]
	if roomReactions == nil {
		ackReactionStoreMu.Unlock()
		return
	}
	entry, ok := roomReactions[sourceEventID]
	if !ok {
		ackReactionStoreMu.Unlock()
		return
	}
	delete(roomReactions, sourceEventID)
	ackReactionStoreMu.Unlock()

	reactionEventID := entry.reactionEventID
	if reactionEventID == "" {
		return
	}

	intent := oc.getModelIntent(ctx, portal)
	if intent == nil {
		return
	}

	// Redact the ack reaction
	_, err := intent.SendMessage(ctx, portal.MXID, event.EventRedaction, &event.Content{
		Parsed: &event.RedactionEventContent{
			Redacts: reactionEventID,
		},
	}, nil)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).
			Stringer("reaction_event", reactionEventID).
			Msg("Failed to remove ack reaction")
	} else {
		oc.loggerForContext(ctx).Debug().
			Stringer("reaction_event", reactionEventID).
			Msg("Removed ack reaction")
	}
}

// handleToolsCommand handles the /tools command for per-tool management
func (oc *AIClient) handleToolsCommand(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	arg string,
) {
	runCtx := oc.backgroundContext(ctx)

	// No args - show status
	if arg == "" {
		oc.showToolsStatus(runCtx, portal, meta)
		return
	}

	parts := strings.SplitN(arg, " ", 2)
	action := strings.ToLower(parts[0])

	switch action {
	case "list":
		oc.showToolsStatus(runCtx, portal, meta)
	case "on", "enable", "true", "1", "off", "disable", "false", "0":
		oc.sendSystemNotice(runCtx, portal, "Per-tool toggles aren't supported anymore. Update tool policy in agent settings or the global tool_policy config.")
	default:
		oc.sendSystemNotice(runCtx, portal, "Usage:\n"+
			"• /tools - Show current tool status\n"+
			"• /tools list - List available tools\n"+
			"Tool toggles are managed by tool policy.")
	}
}

// showToolsStatus displays the current status of all tools
func (oc *AIClient) showToolsStatus(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) {
	oc.sendSystemNotice(ctx, portal, oc.buildToolsStatusText(meta))
}

// handleRegenerate regenerates the last AI response
func (oc *AIClient) handleRegenerate(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
) {
	runCtx := oc.backgroundContext(ctx)

	// Get message history
	history, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(runCtx, portal.PortalKey, 10)
	if err != nil || len(history) == 0 {
		oc.sendSystemNotice(runCtx, portal, "No messages to regenerate from.")
		return
	}

	// Find the last user message
	var lastUserMessage *database.Message
	for _, msg := range history {
		msgMeta := messageMeta(msg)
		if msgMeta != nil && msgMeta.Role == "user" {
			lastUserMessage = msg
			break
		}
	}

	if lastUserMessage == nil {
		oc.sendSystemNotice(runCtx, portal, "No user message found to regenerate from.")
		return
	}

	userMeta := messageMeta(lastUserMessage)
	if userMeta == nil || userMeta.Body == "" {
		oc.sendSystemNotice(runCtx, portal, "Can't regenerate: message content isn't available.")
		return
	}

	oc.sendSystemNotice(runCtx, portal, "Regenerating response...")

	// Build prompt excluding the old assistant response
	prompt, err := oc.buildPromptForRegenerate(runCtx, portal, meta, userMeta.Body, lastUserMessage.MXID)
	if err != nil {
		oc.sendSystemNotice(runCtx, portal, "Couldn't regenerate: "+err.Error())
		return
	}

	queueSettings, _, _, _ := oc.resolveQueueSettingsForPortal(runCtx, portal, meta, "", QueueInlineOptions{})
	isGroup := oc.isGroupChat(runCtx, portal)
	pending := pendingMessage{
		Event:         evt,
		Portal:        portal,
		Meta:          meta,
		Type:          pendingTypeRegenerate,
		MessageBody:   userMeta.Body,
		SourceEventID: lastUserMessage.MXID,
		Typing: &TypingContext{
			IsGroup:      isGroup,
			WasMentioned: true,
		},
	}
	queueItem := pendingQueueItem{
		pending:     pending,
		messageID:   string(evt.ID),
		summaryLine: userMeta.Body,
		enqueuedAt:  time.Now().UnixMilli(),
	}
	oc.dispatchOrQueueWithStatus(runCtx, evt, portal, meta, queueItem, queueSettings, prompt)
}

// handleRegenerateTitle regenerates the current room title from recent messages.
func (oc *AIClient) handleRegenerateTitle(
	ctx context.Context,
	portal *bridgev2.Portal,
) {
	runCtx := oc.backgroundContext(ctx)

	history, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(runCtx, portal.PortalKey, 20)
	if err != nil || len(history) == 0 {
		oc.sendSystemNotice(runCtx, portal, "No messages to generate a title from.")
		return
	}

	var lastUserMessage *database.Message
	var lastAssistantMessage *database.Message
	for _, msg := range history {
		msgMeta := messageMeta(msg)
		if !shouldIncludeInHistory(msgMeta) {
			continue
		}
		if lastAssistantMessage == nil && msgMeta.Role == "assistant" {
			lastAssistantMessage = msg
		}
		if lastUserMessage == nil && msgMeta.Role == "user" {
			lastUserMessage = msg
		}
		if lastUserMessage != nil && lastAssistantMessage != nil {
			break
		}
	}

	if lastUserMessage == nil {
		oc.sendSystemNotice(runCtx, portal, "No user message found to generate a title from.")
		return
	}

	userMeta := messageMeta(lastUserMessage)
	if userMeta == nil || userMeta.Body == "" {
		oc.sendSystemNotice(runCtx, portal, "Can't generate a title: message content isn't available.")
		return
	}

	assistantBody := ""
	if lastAssistantMessage != nil {
		assistantMeta := messageMeta(lastAssistantMessage)
		if assistantMeta != nil {
			assistantBody = assistantMeta.Body
		}
	}

	oc.sendSystemNotice(runCtx, portal, "Regenerating title...")

	title, err := oc.generateRoomTitle(runCtx, userMeta.Body, assistantBody)
	if err != nil {
		oc.sendSystemNotice(runCtx, portal, "Couldn't generate a title: "+err.Error())
		return
	}

	title = strings.TrimSpace(title)
	if title == "" {
		oc.sendSystemNotice(runCtx, portal, "Couldn't generate a title: empty response.")
		return
	}

	if err := oc.setRoomName(runCtx, portal, title); err != nil {
		oc.sendSystemNotice(runCtx, portal, "Couldn't set the room title: "+err.Error())
		return
	}

	oc.sendSystemNotice(runCtx, portal, fmt.Sprintf("Room title updated to: %s", title))
}

// buildPromptForRegenerate builds a prompt for regeneration, excluding the last assistant message
func (oc *AIClient) buildPromptForRegenerate(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	latestUserBody string,
	latestUserID id.EventID,
) ([]openai.ChatCompletionMessageParamUnion, error) {
	var prompt []openai.ChatCompletionMessageParamUnion
	systemPrompt := oc.effectivePrompt(meta)
	if systemPrompt != "" {
		prompt = append(prompt, openai.SystemMessage(systemPrompt))
	}
	prompt = append(prompt, oc.buildAdditionalSystemPrompts(ctx, portal, meta)...)

	historyLimit := oc.historyLimit(ctx, portal, meta)
	resetAt := int64(0)
	if meta != nil {
		resetAt = meta.SessionResetAt
	}
	if historyLimit > 0 {
		history, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, historyLimit+2)
		if err != nil {
			return nil, fmt.Errorf("failed to load prompt history: %w", err)
		}

		// Skip the most recent messages (last user and assistant) and build from older history
		skippedUser := false
		skippedAssistant := false
		for _, msg := range history {
			msgMeta := messageMeta(msg)
			// Skip commands and non-conversation messages
			if !shouldIncludeInHistory(msgMeta) {
				continue
			}
			if resetAt > 0 && msg.Timestamp.UnixMilli() < resetAt {
				continue
			}

			// Skip the last user message and last assistant message
			if !skippedUser && msgMeta.Role == "user" {
				skippedUser = true
				continue
			}
			if !skippedAssistant && msgMeta.Role == "assistant" {
				skippedAssistant = true
				continue
			}

			body := msgMeta.Body
			if msg.MXID != "" {
				body = appendMessageIDHint(msgMeta.Body, msg.MXID)
			}
			switch msgMeta.Role {
			case "assistant":
				prompt = append(prompt, openai.AssistantMessage(body))
			default:
				prompt = append(prompt, openai.UserMessage(body))
			}
		}

		// Reverse to get chronological order (skip system message at index 0 if present)
		startIdx := 0
		if systemPrompt != "" && len(prompt) > 0 {
			startIdx = 1
		}
		for i, j := len(prompt)-1, startIdx; i > j; i, j = i-1, j+1 {
			prompt[i], prompt[j] = prompt[j], prompt[i]
		}
	}

	prompt = append(prompt, openai.UserMessage(appendMessageIDHint(latestUserBody, latestUserID)))
	return prompt, nil
}
