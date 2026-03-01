package connector

import (
	"context"
	"strings"
	"time"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/shared/citations"
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
	replyAccumulator *streamingDirectiveAccumulator
	// If true, prepend a separator before the next non-whitespace text delta.
	// Used when a tool continuation resumes a previously-started assistant message.
	needsTextSeparator bool

	// Heartbeat handling
	heartbeat         *HeartbeatRunConfig
	heartbeatResultCh chan HeartbeatRunOutcome
	suppressSave      bool
	suppressSend      bool

	// AI SDK UIMessage stream tracking (shared across bridges)
	ui streamui.UIState

	// Pending MCP approvals to resolve before the turn can continue.
	pendingMcpApprovals     []mcpApprovalRequest
	pendingMcpApprovalsSeen map[string]bool

	// Avoid logging repeated missing-ephemeral warnings.
	streamEphemeralUnsupported bool

	// Debounced ephemeral logging: true once the "Streaming started" summary has been logged.
	loggedStreamStart bool
}

type mcpApprovalRequest struct {
	approvalID  string
	toolCallID  string
	toolName    string
	serverLabel string
}

// newStreamingState creates a new streaming state with initialized fields
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
		replyAccumulator:        newStreamingDirectiveAccumulator(),
		ui:                      ui,
		pendingMcpApprovalsSeen: make(map[string]bool),
	}
	if meta != nil && normalizeSendPolicyMode(meta.SendPolicy) == "deny" {
		state.suppressSend = true
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

func (oc *AIClient) uiEmitter(state *streamingState) *streamui.Emitter {
	state.ui.TurnID = state.turnID
	state.ui.InitMaps()
	return &streamui.Emitter{
		State: &state.ui,
		Emit: func(ctx context.Context, portal *bridgev2.Portal, part map[string]any) {
			oc.emitStreamEvent(ctx, portal, state, part)
		},
	}
}

func (oc *AIClient) applyStreamingReplyTarget(state *streamingState, parsed *streamingDirectiveResult) {
	if oc == nil || state == nil || parsed == nil || !parsed.HasReplyTag {
		return
	}
	if oc.resolveMatrixReplyToMode() == "off" {
		return
	}
	if parsed.ReplyToExplicitID != "" {
		state.replyTarget.ReplyTo = id.EventID(parsed.ReplyToExplicitID)
		return
	}
	if parsed.ReplyToCurrent && state.sourceEventID != "" {
		state.replyTarget.ReplyTo = state.sourceEventID
	}
}

func (oc *AIClient) markMessageSendSuccess(ctx context.Context, portal *bridgev2.Portal, evt *event.Event, state *streamingState) {
	if state == nil || state.suppressSend {
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
