package connector

import (
	"context"
	"strings"
	"time"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	runtimeparse "github.com/beeper/ai-bridge/pkg/runtime"
	"github.com/beeper/ai-bridge/pkg/shared/citations"
	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
	"github.com/beeper/ai-bridge/pkg/shared/streamui"
)

// streamingState tracks the state of a streaming response
type streamingState struct {
	turnID         string
	agentID        string
	startedAtMs    int64
	firstTokenAtMs int64
	completedAtMs  int64
	roomID         id.RoomID

	promptTokens     int64
	completionTokens int64
	reasoningTokens  int64
	totalTokens      int64

	baseInput              responses.ResponseInputParam
	accumulated            strings.Builder
	visibleAccumulated     strings.Builder
	reasoning              strings.Builder
	toolCalls              []ToolCallMetadata
	pendingImages          []generatedImage
	pendingFunctionOutputs []functionCallOutput // Function outputs to send back to API for continuation
	sourceCitations        []citations.SourceCitation
	sourceDocuments        []citations.SourceDocument
	generatedFiles         []citations.GeneratedFilePart
	initialEventID         id.EventID
	networkMessageID       networkid.MessageID // Network message ID for bridgev2 DB lookup
	finishReason           string
	responseID             string
	sequenceNum            int
	firstToken             bool
	statusSent             bool
	statusSentIDs          map[id.EventID]bool

	// Directive processing
	sourceEventID    id.EventID // The triggering user message event ID (for [[reply_to_current]])
	senderID         string     // The triggering sender ID (for owner-only tool gating)
	replyTarget      ReplyTarget
	replyAccumulator *runtimeparse.StreamingDirectiveAccumulator
	// If true, prepend a separator before the next non-whitespace text delta.
	// Used when a tool continuation resumes a previously-started assistant message.
	needsTextSeparator bool

	// Heartbeat handling
	heartbeat         *HeartbeatRunConfig
	heartbeatResultCh chan HeartbeatRunOutcome
	suppressSave      bool
	suppressSend      bool

	// AI SDK UIMessage stream tracking (shared across bridges)
	ui      streamui.UIState
	emitter *streamui.Emitter
	session *streamtransport.StreamSession

	// Pending MCP approvals to resolve before the turn can continue.
	pendingMcpApprovals     []mcpApprovalRequest
	pendingMcpApprovalsSeen map[string]bool

	// Debounced ephemeral logging: true once the "Streaming started" summary has been logged.
	loggedStreamStart bool
}

func (s *streamingState) hasInitialMessageTarget() bool {
	return s != nil && (s.initialEventID != "" || s.networkMessageID != "")
}

type mcpApprovalRequest struct {
	approvalID  string
	toolCallID  string
	toolName    string
	serverLabel string
}

func newStreamingState(ctx context.Context, meta *PortalMetadata, sourceEventID id.EventID, senderID string, roomID id.RoomID) *streamingState {
	agentID := ""
	if meta != nil {
		agentID = resolveAgentID(meta)
	}
	turnID := NewTurnID()
	ui := streamui.UIState{TurnID: turnID}
	ui.InitMaps()
	state := &streamingState{
		turnID:                  turnID,
		agentID:                 agentID,
		startedAtMs:             time.Now().UnixMilli(),
		firstToken:              true,
		sourceEventID:           sourceEventID,
		senderID:                senderID,
		roomID:                  roomID,
		statusSentIDs:           make(map[id.EventID]bool),
		replyAccumulator:        runtimeparse.NewStreamingDirectiveAccumulator(),
		ui:                      ui,
		pendingMcpApprovalsSeen: make(map[string]bool),
	}
	if hb := heartbeatRunFromContext(ctx); hb != nil {
		state.heartbeat = hb.Config
		state.heartbeatResultCh = hb.ResultCh
		if hb.Config != nil && hb.Config.SuppressSave {
			state.suppressSave = true
		}
		if hb.Config != nil && hb.Config.SuppressSend {
			state.suppressSend = true
		}
	}
	return state
}

func (oc *AIClient) setupEmitter(state *streamingState) {
	if state == nil {
		return
	}
	state.emitter = &streamui.Emitter{
		State: &state.ui,
		Emit: func(ctx context.Context, portal *bridgev2.Portal, part map[string]any) {
			streamui.ApplyChunk(&state.ui, part)
			oc.emitStreamEvent(ctx, portal, state, part)
		},
	}
}

func (oc *AIClient) uiEmitter(state *streamingState) *streamui.Emitter {
	if state != nil && state.emitter != nil {
		return state.emitter
	}
	if state == nil {
		fallback := &streamui.UIState{}
		fallback.InitMaps()
		return &streamui.Emitter{
			State: fallback,
			Emit:  func(context.Context, *bridgev2.Portal, map[string]any) {},
		}
	}
	return &streamui.Emitter{
		State: &state.ui,
		Emit: func(ctx context.Context, portal *bridgev2.Portal, part map[string]any) {
			streamui.ApplyChunk(&state.ui, part)
			oc.emitStreamEvent(ctx, portal, state, part)
		},
	}
}

func (oc *AIClient) applyStreamingReplyTarget(state *streamingState, parsed *runtimeparse.StreamingDirectiveResult) {
	if oc == nil || state == nil || parsed == nil || !parsed.HasReplyTag {
		return
	}
	mode := runtimeparse.NormalizeReplyToMode(oc.resolveMatrixReplyToMode())
	if parsed.ReplyToExplicitID != "" {
		state.replyTarget.ReplyTo = id.EventID(strings.TrimSpace(parsed.ReplyToExplicitID))
	} else if parsed.ReplyToCurrent && state.sourceEventID != "" {
		state.replyTarget.ReplyTo = state.sourceEventID
	}

	applied := runtimeparse.ApplyReplyToMode([]runtimeparse.ReplyPayload{{
		ReplyToID:      state.replyTarget.ReplyTo.String(),
		ReplyToTag:     parsed.HasReplyTag,
		ReplyToCurrent: parsed.ReplyToCurrent,
	}}, runtimeparse.ReplyThreadPolicy{
		Mode:                     mode,
		AllowExplicitWhenModeOff: false,
	})
	if len(applied) == 0 || strings.TrimSpace(applied[0].ReplyToID) == "" {
		state.replyTarget.ReplyTo = ""
		return
	}
	state.replyTarget.ReplyTo = id.EventID(strings.TrimSpace(applied[0].ReplyToID))
}

func (oc *AIClient) finalizeStreamingReplyAccumulator(state *streamingState) {
	if oc == nil || state == nil || state.replyAccumulator == nil {
		return
	}
	parsed := state.replyAccumulator.Consume("", true)
	if parsed == nil {
		return
	}
	oc.applyStreamingReplyTarget(state, parsed)
}

func (oc *AIClient) markMessageSendSuccess(ctx context.Context, portal *bridgev2.Portal, evt *event.Event, state *streamingState) {
	if state == nil || state.suppressSend || state.statusSent {
		return
	}
	if queueAcceptedStatusFromContext(ctx) {
		return
	}
	if state.statusSentIDs == nil {
		state.statusSentIDs = make(map[id.EventID]bool)
	}
	events := make([]*event.Event, 0, 4)
	if evt != nil {
		events = append(events, evt)
	}
	events = append(events, statusEventsFromContext(ctx)...)
	if portal != nil && portal.MXID != "" {
		events = append(events, oc.roomRunStatusEvents(portal.MXID)...)
	}
	for _, extra := range events {
		if extra == nil || extra.ID == "" {
			continue
		}
		if state.statusSentIDs[extra.ID] {
			continue
		}
		oc.sendSuccessStatus(ctx, portal, extra)
		state.statusSentIDs[extra.ID] = true
	}
	if len(state.statusSentIDs) > 0 {
		state.statusSent = true
	}
}

// generatedImage tracks a pending image from image generation
type generatedImage struct {
	itemID   string
	imageB64 string
	turnID   string
}

// functionCallOutput tracks a completed function call output for API continuation
type functionCallOutput struct {
	callID    string // The ItemID from the stream event (used as call_id in continuation)
	name      string // Tool name (for stateless continuations)
	arguments string // Raw arguments JSON (for stateless continuations)
	output    string // The result from executing the tool
}

func buildFunctionCallOutputItem(callID, output string, includeID bool) responses.ResponseInputItemUnionParam {
	item := responses.ResponseInputItemFunctionCallOutputParam{
		CallID: callID,
		Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
			OfString: param.NewOpt(output),
		},
	}
	if includeID {
		item.ID = param.NewOpt("fc_output_" + callID)
	}
	return responses.ResponseInputItemUnionParam{OfFunctionCallOutput: &item}
}

func recordGeneratedFile(state *streamingState, url, mediaType string) {
	if state == nil {
		return
	}
	url = strings.TrimSpace(url)
	if url == "" {
		return
	}
	for _, file := range state.generatedFiles {
		if file.URL == url {
			return
		}
	}
	state.generatedFiles = append(state.generatedFiles, citations.GeneratedFilePart{
		URL:       url,
		MediaType: strings.TrimSpace(mediaType),
	})
}
