package ai

import (
	"context"
	"strings"
	"time"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
	runtimeparse "github.com/beeper/agentremote/pkg/runtime"
	"github.com/beeper/agentremote/pkg/shared/citations"
	"github.com/beeper/agentremote/sdk"
)

// streamingState tracks the state of a streaming response
type streamingState struct {
	turn *sdk.Turn

	agentID         string
	startedAtMs     int64
	lastStreamOrder int64
	firstTokenAtMs  int64
	completedAtMs   int64
	roomID          id.RoomID

	promptTokens     int64
	completionTokens int64
	reasoningTokens  int64
	totalTokens      int64

	baseInput              responses.ResponseInputParam
	accumulated            strings.Builder
	reasoning              strings.Builder
	toolCalls              []ToolCallMetadata
	pendingImages          []generatedImage
	pendingFunctionOutputs []functionCallOutput // Function outputs to send back to API for continuation
	pendingSteeringPrompts []string
	sourceCitations        []citations.SourceCitation
	sourceDocuments        []citations.SourceDocument
	generatedFiles         []citations.GeneratedFilePart
	finishReason           string
	responseID             string
	responseStatus         string
	statusSent             bool
	statusSentIDs          map[id.EventID]bool

	// Directive processing
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

	// Pending MCP approvals to resolve before the turn can continue.
	pendingMcpApprovals     []mcpApprovalRequest
	pendingMcpApprovalsSeen map[string]bool
}

// sourceEventID returns the triggering user message event ID from the turn's source ref.
func (s *streamingState) sourceEventID() id.EventID {
	if s == nil || s.turn == nil || s.turn.Source() == nil {
		return ""
	}
	return id.EventID(s.turn.Source().EventID)
}

// senderID returns the triggering sender ID from the turn's source ref.
func (s *streamingState) senderID() string {
	if s == nil || s.turn == nil || s.turn.Source() == nil {
		return ""
	}
	return s.turn.Source().SenderID
}

func (s *streamingState) hasInitialMessageTarget() bool {
	return s != nil && (s.hasEditTarget() || s.hasEphemeralTarget())
}

func (s *streamingState) hasEditTarget() bool {
	return s != nil && s.turn != nil && s.turn.NetworkMessageID() != ""
}

func (s *streamingState) hasEphemeralTarget() bool {
	return s != nil && s.turn != nil && s.turn.InitialEventID() != ""
}

func (s *streamingState) writer() *sdk.Writer {
	if s == nil || s.turn == nil {
		return nil
	}
	return s.turn.Writer()
}

func (s *streamingState) nextMessageTiming() agentremote.EventTiming {
	if s == nil {
		return agentremote.ResolveEventTiming(time.Time{}, 0)
	}
	ts := time.UnixMilli(s.startedAtMs)
	if s.startedAtMs <= 0 {
		ts = time.Now()
	}
	timing := agentremote.NextEventTiming(s.lastStreamOrder, ts)
	s.lastStreamOrder = timing.StreamOrder
	return timing
}

// clearContinuationState resets pending function outputs and MCP approvals
// after they have been consumed for a continuation round.
func (s *streamingState) clearContinuationState() {
	if s == nil {
		return
	}
	s.pendingFunctionOutputs = nil
	s.pendingMcpApprovals = nil
	s.pendingSteeringPrompts = nil
}

func (s *streamingState) addPendingSteeringPrompts(prompts []string) {
	if s == nil || len(prompts) == 0 {
		return
	}
	s.pendingSteeringPrompts = append(s.pendingSteeringPrompts, prompts...)
}

func (s *streamingState) consumePendingSteeringPrompts() []string {
	if s == nil || len(s.pendingSteeringPrompts) == 0 {
		return nil
	}
	prompts := append([]string(nil), s.pendingSteeringPrompts...)
	s.pendingSteeringPrompts = nil
	return prompts
}

// trackFirstToken records the first-token timestamp once.
func (s *streamingState) trackFirstToken() {
	if s != nil && s.firstTokenAtMs == 0 {
		s.firstTokenAtMs = time.Now().UnixMilli()
	}
}

type mcpApprovalRequest struct {
	approvalID  string
	toolCallID  string
	toolName    string
	serverLabel string
	handle      sdk.ApprovalHandle
}

func newStreamingState(ctx context.Context, meta *PortalMetadata, roomID id.RoomID) *streamingState {
	agentID := ""
	if meta != nil {
		agentID = resolveAgentID(meta)
	}
	state := &streamingState{
		agentID:                 agentID,
		startedAtMs:             time.Now().UnixMilli(),
		roomID:                  roomID,
		statusSentIDs:           make(map[id.EventID]bool),
		replyAccumulator:        runtimeparse.NewStreamingDirectiveAccumulator(),
		pendingMcpApprovalsSeen: make(map[string]bool),
	}
	if hb := heartbeatRunFromContext(ctx); hb != nil {
		state.heartbeat = hb.Config
		state.heartbeatResultCh = hb.ResultCh
		if hb.Config != nil {
			state.suppressSave = hb.Config.SuppressSave
			state.suppressSend = hb.Config.SuppressSend
		}
	}
	return state
}

func (oc *AIClient) applyStreamingReplyTarget(state *streamingState, parsed *runtimeparse.StreamingDirectiveResult) {
	if oc == nil || state == nil || parsed == nil || !parsed.HasReplyTag {
		return
	}
	mode := runtimeparse.NormalizeReplyToMode(oc.resolveMatrixReplyToMode())
	if parsed.ReplyToExplicitID != "" {
		state.replyTarget.ReplyTo = id.EventID(strings.TrimSpace(parsed.ReplyToExplicitID))
	} else if parsed.ReplyToCurrent && state.sourceEventID() != "" {
		state.replyTarget.ReplyTo = state.sourceEventID()
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
