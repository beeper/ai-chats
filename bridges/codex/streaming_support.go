package codex

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/shared/backfillutil"
	"github.com/beeper/agentremote/pkg/shared/citations"
	"github.com/beeper/agentremote/pkg/shared/streamui"
	"github.com/beeper/agentremote/turns"
)

type streamingState struct {
	turnID               string
	agentID              string
	startedAtMs          int64
	firstTokenAtMs       int64
	completedAtMs        int64
	promptTokens         int64
	completionTokens     int64
	reasoningTokens      int64
	totalTokens          int64
	accumulated          strings.Builder
	visibleAccumulated   strings.Builder
	reasoning            strings.Builder
	toolCalls            []ToolCallMetadata
	sourceCitations      []citations.SourceCitation
	sourceDocuments      []citations.SourceDocument
	generatedFiles       []citations.GeneratedFilePart
	initialEventID       id.EventID
	networkMessageID     networkid.MessageID
	sequenceNum          int
	lastRemoteEventOrder int64
	firstToken           bool
	suppressSend         bool

	ui      streamui.UIState
	session *turns.StreamSession

	codexToolOutputBuffers    map[string]*strings.Builder
	codexLatestDiff           string
	codexReasoningSummarySeen bool
	codexTimelineNotices      map[string]bool
	loggedStreamStart         bool
}

func (s *streamingState) hasInitialMessageTarget() bool {
	return s.hasEditTarget()
}

func (s *streamingState) streamTarget() turns.StreamTarget {
	if s == nil {
		return turns.StreamTarget{}
	}
	return turns.StreamTarget{NetworkMessageID: s.networkMessageID}
}

func (s *streamingState) hasEditTarget() bool {
	return s != nil && s.streamTarget().HasEditTarget()
}

func (cc *CodexClient) uiEmitter(state *streamingState) *streamui.Emitter {
	state.ui.TurnID = state.turnID
	state.ui.InitMaps()
	return &streamui.Emitter{
		State: &state.ui,
		Emit: func(ctx context.Context, portal *bridgev2.Portal, part map[string]any) {
			streamui.ApplyChunk(&state.ui, part)
			cc.emitStreamEvent(ctx, portal, state, part)
		},
	}
}

func newStreamingState(_ context.Context, _ *PortalMetadata, sourceEventID id.EventID, _ string, _ id.RoomID) *streamingState {
	turnID := NewTurnID()
	ui := streamui.UIState{TurnID: turnID}
	ui.InitMaps()
	return &streamingState{
		turnID:                 turnID,
		startedAtMs:            time.Now().UnixMilli(),
		firstToken:             true,
		initialEventID:         sourceEventID,
		ui:                     ui,
		codexTimelineNotices:   make(map[string]bool),
		codexToolOutputBuffers: make(map[string]*strings.Builder),
	}
}

func codexStreamEventTimestamp(state *streamingState, preferCompleted bool) time.Time {
	if state == nil {
		return time.Now()
	}
	if preferCompleted && state.completedAtMs > 0 {
		return time.UnixMilli(state.completedAtMs)
	}
	if state.startedAtMs > 0 {
		return time.UnixMilli(state.startedAtMs)
	}
	if state.completedAtMs > 0 {
		return time.UnixMilli(state.completedAtMs)
	}
	return time.Now()
}

func codexNextLiveStreamOrder(state *streamingState, ts time.Time) int64 {
	if state == nil {
		return backfillutil.NextStreamOrder(0, ts)
	}
	state.lastRemoteEventOrder = backfillutil.NextStreamOrder(state.lastRemoteEventOrder, ts)
	return state.lastRemoteEventOrder
}
