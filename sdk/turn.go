package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/pkg/shared/citations"
	"github.com/beeper/agentremote/pkg/shared/streamui"
	"github.com/beeper/agentremote/turns"
)

// Stream is a type alias for Turn, preserved for backward compatibility.
type Stream = Turn

type sdkApprovalHandle struct {
	approvalID string
	toolCallID string
	turn       *Turn
}

func (h *sdkApprovalHandle) ID() string {
	if h == nil {
		return ""
	}
	return h.approvalID
}

func (h *sdkApprovalHandle) ToolCallID() string {
	if h == nil {
		return ""
	}
	return h.toolCallID
}

func (h *sdkApprovalHandle) Wait(ctx context.Context) (ToolApprovalResponse, error) {
	if h == nil || h.turn == nil || h.turn.conv == nil || h.turn.turnCtx == nil {
		return ToolApprovalResponse{}, nil
	}
	runtime := h.turn.conv.runtime
	if runtime == nil || runtime.approvalFlowValue() == nil {
		return ToolApprovalResponse{}, nil
	}
	approvalFlow := runtime.approvalFlowValue()
	decision, ok := approvalFlow.Wait(ctx, h.approvalID)
	if !ok {
		reason := agentremote.ApprovalReasonTimeout
		if ctx != nil && ctx.Err() != nil {
			reason = agentremote.ApprovalReasonCancelled
		}
		h.turn.emitter.EmitUIToolApprovalResponse(h.turn.turnCtx, h.turn.conv.portal, h.approvalID, h.toolCallID, false, reason)
		approvalFlow.FinishResolved(h.approvalID, agentremote.ApprovalDecisionPayload{
			ApprovalID: h.approvalID,
			Reason:     reason,
		})
		return ToolApprovalResponse{Reason: reason}, nil
	}
	h.turn.emitter.EmitUIToolApprovalResponse(h.turn.turnCtx, h.turn.conv.portal, h.approvalID, h.toolCallID, decision.Approved, decision.Reason)
	approvalFlow.FinishResolved(h.approvalID, decision)
	return ToolApprovalResponse{
		Approved: decision.Approved,
		Always:   decision.Always,
		Reason:   decision.Reason,
	}, nil
}

// Turn is the central abstraction for an AI response turn.
type Turn struct {
	ctx     context.Context
	turnCtx context.Context
	cancel  context.CancelFunc

	conv    *Conversation
	emitter *streamui.Emitter
	state   *streamui.UIState
	session *turns.StreamSession
	turnID  string

	started bool
	ended   bool

	agent  *Agent
	source *SourceRef

	replyTo     id.EventID
	threadRoot  id.EventID
	startedAtMs int64

	sender           bridgev2.EventSender
	networkMessageID networkid.MessageID
	initialEventID   id.EventID
	sessionOnce      sync.Once

	visibleText strings.Builder
	metadata    map[string]any
	startErr    error
	mu          sync.Mutex

	streamHook           func(turnID string, seq int, content map[string]any, txnID string) bool
	approvalRequester    func(ctx context.Context, turn *Turn, req ApprovalRequest) ApprovalHandle
	finalMetadataBuilder func(turn *Turn, finishReason string) any
}

func newTurn(ctx context.Context, conv *Conversation, agent *Agent, source *SourceRef) *Turn {
	if ctx == nil {
		ctx = context.Background()
	}
	turnCtx, cancel := context.WithCancel(ctx)
	turnID := uuid.NewString()
	state := &streamui.UIState{TurnID: turnID}
	state.InitMaps()

	t := &Turn{
		ctx:         ctx,
		turnCtx:     turnCtx,
		cancel:      cancel,
		conv:        conv,
		state:       state,
		turnID:      turnID,
		agent:       agent,
		source:      source,
		startedAtMs: time.Now().UnixMilli(),
		metadata:    make(map[string]any),
	}

	t.emitter = &streamui.Emitter{
		State: state,
		Emit: func(callCtx context.Context, portal *bridgev2.Portal, part map[string]any) {
			streamui.ApplyChunk(t.state, part)
			if t.session != nil {
				t.session.EmitPart(callCtx, part)
			}
		},
	}
	return t
}

func (t *Turn) providerIdentity() ProviderIdentity {
	if t.conv != nil && t.conv.runtime != nil {
		return t.conv.runtime.providerIdentity()
	}
	return defaultProviderIdentity()
}

func (t *Turn) resolveAgent(ctx context.Context) *Agent {
	if t.agent != nil {
		return t.agent
	}
	if t.conv == nil {
		return nil
	}
	agent, _ := t.conv.resolveDefaultAgent(ctx)
	return agent
}

func (t *Turn) resolveSender(ctx context.Context) bridgev2.EventSender {
	if t.sender.Sender != "" || t.sender.IsFromMe {
		return t.sender
	}
	if agent := t.resolveAgent(ctx); agent != nil && t.conv != nil && t.conv.login != nil {
		t.sender = agent.EventSender(t.conv.login.ID)
		return t.sender
	}
	if t.conv != nil {
		t.sender = t.conv.sender
	}
	return t.sender
}

func (t *Turn) buildPlaceholderMessage() *bridgev2.ConvertedMessage {
	raw := map[string]any{
		"msgtype":    event.MsgText,
		"body":       "...",
		"m.mentions": map[string]any{},
	}
	if relatesTo := t.buildRelatesTo(); relatesTo != nil {
		raw["m.relates_to"] = relatesTo
	}
	return &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:   networkid.PartID("0"),
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				MsgType: event.MsgText,
				Body:    "...",
			},
			Extra: raw,
		}},
	}
}

func (t *Turn) buildRelatesTo() map[string]any {
	if t.threadRoot != "" {
		replyTo := t.replyTo
		if replyTo == "" && t.source != nil && t.source.EventID != "" {
			replyTo = id.EventID(t.source.EventID)
		}
		rel := map[string]any{
			"rel_type":        "m.thread",
			"event_id":        t.threadRoot.String(),
			"is_falling_back": true,
		}
		if replyTo != "" {
			rel["m.in_reply_to"] = map[string]any{
				"event_id": replyTo.String(),
			}
		}
		return rel
	}
	if t.replyTo != "" {
		return map[string]any{
			"m.in_reply_to": map[string]any{
				"event_id": t.replyTo.String(),
			},
		}
	}
	if t.source != nil && t.source.EventID != "" {
		return map[string]any{
			"event_id": id.EventID(t.source.EventID).String(),
		}
	}
	return nil
}

func (t *Turn) ensureSession() {
	t.sessionOnce.Do(func() {
		var logger zerolog.Logger
		if t.conv != nil && t.conv.login != nil {
			logger = t.conv.login.Log.With().Str("component", "sdk_turn").Logger()
		}
		sender := t.resolveSender(t.turnCtx)
		identity := t.providerIdentity()
		t.session = turns.NewStreamSession(turns.StreamSessionParams{
			TurnID:  t.turnID,
			AgentID: strings.TrimSpace(string(sender.Sender)),
			GetStreamTarget: func() turns.StreamTarget {
				return turns.StreamTarget{NetworkMessageID: t.networkMessageID}
			},
			ResolveTargetEventID: func(callCtx context.Context, target turns.StreamTarget) (id.EventID, error) {
				if t.conv == nil || t.conv.login == nil || t.conv.login.Bridge == nil {
					return "", nil
				}
				return turns.ResolveTargetEventIDFromDB(callCtx, t.conv.login.Bridge, t.conv.portal.Receiver, target)
			},
			GetRoomID: func() id.RoomID {
				if t.conv == nil || t.conv.portal == nil {
					return ""
				}
				return t.conv.portal.MXID
			},
			GetSuppressSend: func() bool { return false },
			NextSeq: func() int {
				t.mu.Lock()
				defer t.mu.Unlock()
				state := t.state
				state.InitMaps()
				state.UIStepCount++
				return state.UIStepCount
			},
			RuntimeFallbackFlag: &atomic.Bool{},
			GetEphemeralSender: func(callCtx context.Context) (bridgev2.EphemeralSendingMatrixAPI, bool) {
				if t.conv == nil || t.conv.login == nil || t.conv.login.Bridge == nil || t.conv.login.Bridge.Bot == nil {
					return nil, false
				}
				ephemeralSender, ok := any(t.conv.login.Bridge.Bot).(bridgev2.EphemeralSendingMatrixAPI)
				return ephemeralSender, ok
			},
			SendDebouncedEdit: func(callCtx context.Context, force bool) error {
				if t.conv == nil || t.conv.login == nil || t.conv.portal == nil {
					return nil
				}
				uiMessage := streamui.SnapshotCanonicalUIMessage(t.state)
				return agentremote.SendDebouncedStreamEdit(agentremote.SendDebouncedStreamEditParams{
					Login:            t.conv.login,
					Portal:           t.conv.portal,
					Sender:           t.resolveSender(callCtx),
					NetworkMessageID: t.networkMessageID,
					VisibleBody:      strings.TrimSpace(t.visibleText.String()),
					FallbackBody:     strings.TrimSpace(t.visibleText.String()),
					LogKey:           identity.LogKey,
					Force:            force,
					UIMessage:        uiMessage,
				})
			},
			SendHook: t.streamHook,
			Logger:   &logger,
		})
	})
}

func (t *Turn) ensureStarted() {
	if t.started || t.ended {
		return
	}
	t.started = true
	if t.conv != nil {
		if agent := t.resolveAgent(t.turnCtx); agent != nil {
			t.agent = agent
			if err := t.conv.EnsureRoomAgent(t.turnCtx, agent); err != nil && t.startErr == nil {
				t.startErr = err
			}
		}
	}
	t.ensureSession()
	if t.conv != nil && t.conv.portal != nil && t.conv.login != nil {
		identity := t.providerIdentity()
		evtID, msgID, err := agentremote.SendViaPortal(agentremote.SendViaPortalParams{
			Login:     t.conv.login,
			Portal:    t.conv.portal,
			Sender:    t.resolveSender(t.turnCtx),
			IDPrefix:  identity.IDPrefix,
			LogKey:    identity.LogKey,
			Timestamp: time.Now(),
			Converted: t.buildPlaceholderMessage(),
		})
		if err == nil {
			t.initialEventID = evtID
			t.networkMessageID = msgID
		} else if t.startErr == nil {
			t.startErr = err
		}
	}
	baseMeta := map[string]any{
		"turnId": t.turnID,
	}
	if agent := t.resolveAgent(t.turnCtx); agent != nil {
		baseMeta["agentId"] = agent.ID
		if agent.ModelKey != "" {
			baseMeta["modelKey"] = agent.ModelKey
		}
	}
	t.emitter.EmitUIStart(t.turnCtx, t.conv.portal, baseMeta)
}

// WriteText sends a text chunk.
func (t *Turn) WriteText(text string) {
	t.ensureStarted()
	t.visibleText.WriteString(text)
	t.emitter.EmitUITextDelta(t.turnCtx, t.conv.portal, text)
}

// WriteReasoning sends a reasoning/thinking chunk.
func (t *Turn) WriteReasoning(text string) {
	t.ensureStarted()
	t.emitter.EmitUIReasoningDelta(t.turnCtx, t.conv.portal, text)
}

// FinishText closes the current text stream part, if one is open.
func (t *Turn) FinishText() {
	t.ensureStarted()
	if t.state == nil || t.state.UITextID == "" {
		return
	}
	partID := t.state.UITextID
	t.emitter.Emit(t.turnCtx, t.conv.portal, map[string]any{
		"type": "text-end",
		"id":   partID,
	})
	t.state.UITextID = ""
}

// FinishReasoning closes the current reasoning stream part, if one is open.
func (t *Turn) FinishReasoning() {
	t.ensureStarted()
	if t.state == nil || t.state.UIReasoningID == "" {
		return
	}
	partID := t.state.UIReasoningID
	t.emitter.Emit(t.turnCtx, t.conv.portal, map[string]any{
		"type": "reasoning-end",
		"id":   partID,
	})
	t.state.UIReasoningID = ""
}

// ToolStart begins a tool call.
func (t *Turn) ToolStart(toolName, toolCallID string, providerExecuted bool) {
	t.ensureStarted()
	t.emitter.EnsureUIToolInputStart(t.turnCtx, t.conv.portal, toolCallID, toolName, providerExecuted, toolName, nil)
}

// ToolInputDelta sends a streaming tool input argument chunk.
func (t *Turn) ToolInputDelta(toolCallID, delta string) {
	t.ensureStarted()
	t.emitter.EmitUIToolInputDelta(t.turnCtx, t.conv.portal, toolCallID, "", delta, false)
}

// ToolInput sends the complete tool input.
func (t *Turn) ToolInput(toolCallID string, input any) {
	t.ensureStarted()
	t.emitter.EmitUIToolInputAvailable(t.turnCtx, t.conv.portal, toolCallID, "", input, false)
}

// ToolOutput sends the tool execution result.
func (t *Turn) ToolOutput(toolCallID string, output any) {
	t.ensureStarted()
	t.emitter.EmitUIToolOutputAvailable(t.turnCtx, t.conv.portal, toolCallID, output, false, false)
}

// ToolOutputError reports a tool execution error.
func (t *Turn) ToolOutputError(toolCallID, errorText string) {
	t.ensureStarted()
	t.emitter.EmitUIToolOutputError(t.turnCtx, t.conv.portal, toolCallID, errorText, false)
}

// ToolDenied reports that the tool execution was denied by the user.
func (t *Turn) ToolDenied(toolCallID string) {
	t.ensureStarted()
	t.emitter.EmitUIToolOutputDenied(t.turnCtx, t.conv.portal, toolCallID)
}

// RequestApproval creates a new approval request and returns its handle.
func (t *Turn) RequestApproval(req ApprovalRequest) ApprovalHandle {
	t.ensureStarted()
	if t.approvalRequester != nil {
		return t.approvalRequester(t.turnCtx, t, req)
	}
	if t.conv == nil || t.conv.portal == nil || t.conv.runtime == nil || t.conv.runtime.approvalFlowValue() == nil {
		return &sdkApprovalHandle{turn: t, toolCallID: req.ToolCallID}
	}
	approvalFlow := t.conv.runtime.approvalFlowValue()
	approvalID := strings.TrimSpace(req.ApprovalID)
	if approvalID == "" {
		approvalID = "sdk-" + uuid.NewString()
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = agentremote.DefaultApprovalExpiry
	}
	_, _ = approvalFlow.Register(approvalID, ttl, &pendingSDKApprovalData{
		RoomID:     t.conv.portal.MXID,
		TurnID:     t.turnID,
		ToolCallID: req.ToolCallID,
		ToolName:   req.ToolName,
	})
	t.emitter.EmitUIToolApprovalRequest(t.turnCtx, t.conv.portal, approvalID, req.ToolCallID)
	presentation := agentremote.ApprovalPromptPresentation{
		Title:       req.ToolName,
		AllowAlways: true,
	}
	if req.Presentation != nil {
		presentation = *req.Presentation
	}
	approvalFlow.SendPrompt(t.turnCtx, t.conv.portal, agentremote.SendPromptParams{
		ApprovalPromptMessageParams: agentremote.ApprovalPromptMessageParams{
			ApprovalID:   approvalID,
			ToolCallID:   req.ToolCallID,
			ToolName:     req.ToolName,
			TurnID:       t.turnID,
			Presentation: presentation,
			ExpiresAt:    time.Now().Add(ttl),
		},
		RoomID:    t.conv.portal.MXID,
		OwnerMXID: t.conv.login.UserMXID,
	})
	return &sdkApprovalHandle{approvalID: approvalID, toolCallID: req.ToolCallID, turn: t}
}

// AddSourceURL adds a source citation URL.
func (t *Turn) AddSourceURL(url, title string) {
	t.ensureStarted()
	t.emitter.EmitUISourceURL(t.turnCtx, t.conv.portal, citations.SourceCitation{
		URL:   url,
		Title: title,
	})
}

// AddSourceDocument adds a source document citation.
func (t *Turn) AddSourceDocument(docID, title, mediaType, filename string) {
	t.ensureStarted()
	t.emitter.EmitUISourceDocument(t.turnCtx, t.conv.portal, citations.SourceDocument{
		ID:        docID,
		Title:     title,
		MediaType: mediaType,
		Filename:  filename,
	})
}

// AddFile adds a generated file reference.
func (t *Turn) AddFile(url, mediaType string) {
	t.ensureStarted()
	t.emitter.EmitUIFile(t.turnCtx, t.conv.portal, url, mediaType)
}

// StepStart begins a visual step grouping.
func (t *Turn) StepStart() {
	t.ensureStarted()
	t.emitter.EmitUIStepStart(t.turnCtx, t.conv.portal)
}

// StepFinish ends a visual step grouping.
func (t *Turn) StepFinish() {
	t.ensureStarted()
	t.emitter.EmitUIStepFinish(t.turnCtx, t.conv.portal)
}

// SetMetadata merges message metadata for this turn.
func (t *Turn) SetMetadata(metadata map[string]any) {
	t.ensureStarted()
	for k, v := range metadata {
		t.metadata[k] = v
	}
	t.emitter.EmitUIMessageMetadata(t.turnCtx, t.conv.portal, metadata)
}

// SetReplyTo sets the m.in_reply_to relation for this turn's message.
func (t *Turn) SetReplyTo(eventID id.EventID) {
	t.replyTo = eventID
}

// SetThread sets the m.thread relation for this turn's message.
func (t *Turn) SetThread(rootEventID id.EventID) {
	t.threadRoot = rootEventID
}

// SetStreamHook captures stream envelopes instead of sending ephemeral Matrix events when provided.
func (t *Turn) SetStreamHook(hook func(turnID string, seq int, content map[string]any, txnID string) bool) {
	t.streamHook = hook
}

// SetApprovalRequester overrides the default SDK approval flow for this turn.
func (t *Turn) SetApprovalRequester(requester func(ctx context.Context, turn *Turn, req ApprovalRequest) ApprovalHandle) {
	t.approvalRequester = requester
}

// SetFinalMetadataBuilder overrides the final DB metadata object persisted for the assistant message.
func (t *Turn) SetFinalMetadataBuilder(builder func(turn *Turn, finishReason string) any) {
	t.finalMetadataBuilder = builder
}

// SendStatus emits a bridge-level status update for the source event when possible.
func (t *Turn) SendStatus(status event.MessageStatus, message string) {
	if t.conv == nil || t.conv.portal == nil || t.conv.login == nil || t.source == nil || t.source.EventID == "" {
		return
	}
	identity := t.providerIdentity()
	_, _ = t.conv.login.Bridge.Bot.SendMessage(t.turnCtx, t.conv.portal.MXID, event.BeeperMessageStatus, &event.Content{
		Parsed: &event.BeeperMessageStatusEventContent{
			Network:   identity.StatusNetwork,
			RelatesTo: event.RelatesTo{EventID: id.EventID(t.source.EventID)},
			Status:    status,
			Message:   message,
		},
	}, nil)
}

func (t *Turn) finalMetadata(finishReason string) agentremote.BaseMessageMetadata {
	uiMessage := streamui.SnapshotCanonicalUIMessage(t.state)
	var agentID string
	if t.agent != nil {
		agentID = t.agent.ID
	}
	runtimeMeta := agentremote.BuildAssistantBaseMetadata(agentremote.AssistantMetadataParams{
		Body:               strings.TrimSpace(t.visibleText.String()),
		FinishReason:       finishReason,
		TurnID:             t.turnID,
		AgentID:            agentID,
		StartedAtMs:        t.startedAtMs,
		CompletedAtMs:      time.Now().UnixMilli(),
		CanonicalSchema:    "com.beeper.ai.message",
		CanonicalUIMessage: uiMessage,
	})
	merged := supportedBaseMetadataFromMap(t.metadata)
	merged.CopyFromBase(&runtimeMeta)
	return merged
}

func (t *Turn) persistFinalMessage(finishReason string) {
	if t.conv == nil || t.conv.login == nil || t.conv.portal == nil {
		return
	}
	sender := t.resolveSender(t.turnCtx)
	metadata := any(t.finalMetadata(finishReason))
	if t.finalMetadataBuilder != nil {
		if custom := t.finalMetadataBuilder(t, finishReason); custom != nil {
			metadata = custom
		}
	}
	agentremote.UpsertAssistantMessage(t.turnCtx, agentremote.UpsertAssistantMessageParams{
		Login:            t.conv.login,
		Portal:           t.conv.portal,
		SenderID:         sender.Sender,
		NetworkMessageID: t.networkMessageID,
		InitialEventID:   t.initialEventID,
		Metadata:         metadata,
		Logger:           t.conv.login.Log.With().Str("component", "sdk_turn").Logger(),
	})
}

func supportedBaseMetadataFromMap(metadata map[string]any) agentremote.BaseMessageMetadata {
	if len(metadata) == 0 {
		return agentremote.BaseMessageMetadata{}
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return agentremote.BaseMessageMetadata{}
	}
	var decoded agentremote.BaseMessageMetadata
	if err = json.Unmarshal(data, &decoded); err != nil {
		return agentremote.BaseMessageMetadata{}
	}
	return decoded
}

// End finishes the turn with a reason.
func (t *Turn) End(finishReason string) {
	if t.ended {
		return
	}
	defer t.cancel()
	if !t.started {
		t.ended = true
		return
	}
	t.ended = true
	t.emitter.EmitUIFinish(t.turnCtx, t.conv.portal, finishReason, t.metadata)
	if t.session != nil {
		t.session.End(t.turnCtx, turns.EndReasonFinish)
	}
	t.persistFinalMessage(finishReason)
}

// EndWithError finishes the turn with an error.
func (t *Turn) EndWithError(errText string) {
	if t.ended {
		return
	}
	defer t.cancel()
	t.ended = true
	if !t.started {
		// No content was ever written — skip placeholder message creation.
		// Still send a fail status if we have a source event.
		t.SendStatus(event.MessageStatusFail, errText)
		return
	}
	t.emitter.EmitUIError(t.turnCtx, t.conv.portal, errText)
	t.emitter.EmitUIFinish(t.turnCtx, t.conv.portal, "error", t.metadata)
	if t.session != nil {
		t.session.End(t.turnCtx, turns.EndReasonError)
	}
	t.persistFinalMessage("error")
}

// Abort aborts the turn.
func (t *Turn) Abort(reason string) {
	if t.ended {
		return
	}
	defer t.cancel()
	t.ended = true
	if !t.started {
		// No content was ever written — skip placeholder message creation.
		t.SendStatus(event.MessageStatusRetriable, reason)
		return
	}
	t.emitter.EmitUIAbort(t.turnCtx, t.conv.portal, reason)
	if t.session != nil {
		t.session.End(t.turnCtx, turns.EndReasonDisconnect)
	}
	t.persistFinalMessage("abort")
}

// ID returns the turn's unique identifier.
func (t *Turn) ID() string { return t.turnID }

// SetID overrides the turn identifier before the turn starts. Provider bridges
// can use this to preserve upstream turn/message IDs in SDK-managed streams.
func (t *Turn) SetID(turnID string) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" || t.started {
		return
	}
	t.turnID = turnID
	if t.state != nil {
		t.state.TurnID = turnID
	}
}

// Context returns the turn-scoped context.
func (t *Turn) Context() context.Context { return t.turnCtx }

// Source returns the turn's structured source reference.
func (t *Turn) Source() *SourceRef { return t.source }

// Agent returns the turn's selected agent.
func (t *Turn) Agent() *Agent { return t.agent }

// SetSender overrides the bridge sender used for turn output. Call before the
// turn produces visible output.
func (t *Turn) SetSender(sender bridgev2.EventSender) { t.sender = sender }

// Emitter returns the underlying streamui.Emitter for escape hatch access.
func (t *Turn) Emitter() *streamui.Emitter { return t.emitter }

// UIState returns the underlying streamui.UIState.
func (t *Turn) UIState() *streamui.UIState { return t.state }

// Session returns the underlying turns.StreamSession.
func (t *Turn) Session() *turns.StreamSession { return t.session }

// Err returns any startup error encountered by the turn transport.
func (t *Turn) Err() error {
	if t.startErr == nil {
		return nil
	}
	return fmt.Errorf("turn startup failed: %w", t.startErr)
}
