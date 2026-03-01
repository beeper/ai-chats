package codex

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/shared/citations"
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
	sequenceNum        int
	firstToken         bool
	suppressSend       bool

	ui streamui.UIState

	codexToolOutputBuffers    map[string]*strings.Builder
	codexLatestDiff           string
	codexReasoningSummarySeen bool
	codexTimelineNotices      map[string]bool
}

func (cc *CodexClient) uiEmitter(state *streamingState) *streamui.Emitter {
	return &streamui.Emitter{
		State: &state.ui,
		Emit: func(ctx context.Context, portal *bridgev2.Portal, part map[string]any) {
			cc.emitStreamEvent(ctx, portal, state, part)
		},
	}
}

func newStreamingState(ctx context.Context, meta *PortalMetadata, sourceEventID id.EventID, senderID string, roomID id.RoomID) *streamingState {
	_ = ctx
	_ = meta
	_ = senderID
	_ = roomID
	turnID := NewTurnID()
	ui := streamui.UIState{TurnID: turnID}
	ui.InitMaps()
	return &streamingState{
		turnID:                 turnID,
		startedAtMs:            nowMillis(),
		firstToken:             true,
		initialEventID:         sourceEventID,
		ui:                     ui,
		codexTimelineNotices:   make(map[string]bool),
		codexToolOutputBuffers: make(map[string]*strings.Builder),
	}
}
