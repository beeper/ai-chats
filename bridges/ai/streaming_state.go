package ai

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

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

	respondingGhostID      string
	respondingAgentID      string
	respondingModelID      string
	respondingContextLimit int

	promptTokens     int64
	completionTokens int64
	reasoningTokens  int64
	totalTokens      int64

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
	currentUserMessage     string
	// Directive processing
	replyTarget      ReplyTarget
	replyAccumulator *runtimeparse.StreamingDirectiveAccumulator
	// If true, prepend a separator before the next non-whitespace text delta.
	// Used when a tool continuation resumes a previously-started assistant message.
	needsTextSeparator bool

	suppressSave bool
	suppressSend bool

	finalized atomic.Bool
	accepted  atomic.Bool
	stop      atomic.Pointer[assistantStopMetadata]
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

func (s *streamingState) markFinalized() bool {
	if s == nil {
		return false
	}
	return s.finalized.CompareAndSwap(false, true)
}

func (s *streamingState) isFinalized() bool {
	if s == nil {
		return false
	}
	return s.finalized.Load()
}

func (s *streamingState) markAccepted() bool {
	if s == nil {
		return false
	}
	return s.accepted.CompareAndSwap(false, true)
}

func (s *streamingState) isAccepted() bool {
	if s == nil {
		return false
	}
	return s.accepted.Load()
}

func (s *streamingState) nextMessageTiming() sdk.EventTiming {
	if s == nil {
		return sdk.ResolveEventTiming(time.Time{}, 0)
	}
	ts := time.UnixMilli(s.startedAtMs)
	if s.startedAtMs <= 0 {
		ts = time.Now()
	}
	timing := sdk.NextEventTiming(s.lastStreamOrder, ts)
	s.lastStreamOrder = timing.StreamOrder
	return timing
}

func (s *streamingState) resetFinishReason() {
	if s == nil {
		return
	}
	s.finishReason = ""
}

func (s *streamingState) setTerminalFailure(reason string) {
	if s == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "error"
	}
	s.finishReason = reason
	s.completedAtMs = time.Now().UnixMilli()
}

func (s *streamingState) finalizeTerminalSuccess() string {
	if s == nil {
		return ""
	}
	s.completedAtMs = time.Now().UnixMilli()
	if s.finishReason == "" {
		s.finishReason = "stop"
	}
	if s.responseStatus == "" && s.responseID != "" {
		s.responseStatus = canonicalResponseStatus(s)
	}
	return s.finishReason
}

func (s *streamingState) applyResponseLifecycleEvent(eventType string, response responses.Response) bool {
	if s == nil {
		return false
	}
	if responseID := strings.TrimSpace(response.ID); responseID != "" {
		s.responseID = responseID
	}
	if status := strings.TrimSpace(string(response.Status)); status != "" {
		s.responseStatus = status
	}

	switch eventType {
	case "response.completed":
		if s.responseStatus == "completed" {
			s.finishReason = "stop"
		} else {
			s.finishReason = s.responseStatus
		}
	case "response.failed":
		s.finishReason = "error"
	case "response.incomplete":
		s.finishReason = strings.TrimSpace(string(response.IncompleteDetails.Reason))
		if s.finishReason == "" {
			s.finishReason = "other"
		}
	case "response.created", "response.queued", "response.in_progress":
		// No terminal state changes needed.
	default:
		return false
	}

	return true
}

// clearContinuationState resets pending continuation state after it has been
// consumed for a continuation round.
func (s *streamingState) clearContinuationState() {
	if s == nil {
		return
	}
	s.pendingFunctionOutputs = nil
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

func newStreamingState(ctx context.Context, meta *PortalMetadata, roomID id.RoomID) *streamingState {
	agentID := ""
	if meta != nil {
		agentID = resolveAgentID(meta)
	}
	state := &streamingState{
		agentID:          agentID,
		startedAtMs:      time.Now().UnixMilli(),
		roomID:           roomID,
		replyAccumulator: runtimeparse.NewStreamingDirectiveAccumulator(),
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

func (oc *AIClient) markTurnAccepted(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata) {
	if state == nil || !state.markAccepted() {
		return
	}
	if !state.suppressSend {
		oc.acceptPendingMessages(ctx, portal, state)
	}
	if writer := state.writer(); writer != nil {
		writer.Start(ctx, oc.buildUIMessageMetadata(state, meta, false))
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
