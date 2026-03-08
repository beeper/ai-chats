package codex

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/shared/citations"
	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
	"github.com/beeper/ai-bridge/pkg/shared/streamui"
)

type streamingState struct {
	turnID             string
	agentID            string
	startedAtMs        int64
	firstTokenAtMs     int64
	completedAtMs      int64
	promptTokens       int64
	completionTokens   int64
	reasoningTokens    int64
	totalTokens        int64
	accumulated        strings.Builder
	visibleAccumulated strings.Builder
	reasoning          strings.Builder
	toolCalls          []ToolCallMetadata
	sourceCitations    []citations.SourceCitation
	sourceDocuments    []citations.SourceDocument
	generatedFiles     []citations.GeneratedFilePart
	initialEventID     id.EventID
	networkMessageID   networkid.MessageID
	sequenceNum        int
	firstToken         bool
	suppressSend       bool

	ui      streamui.UIState
	session *streamtransport.StreamSession

	codexToolOutputBuffers    map[string]*strings.Builder
	codexLatestDiff           string
	codexReasoningSummarySeen bool
	codexTimelineNotices      map[string]bool
	loggedStreamStart         bool
}

func (s *streamingState) hasInitialMessageTarget() bool {
	return s != nil && (s.initialEventID != "" || s.networkMessageID != "")
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
