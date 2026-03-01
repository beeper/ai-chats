package codex

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/matrixevents"
	"github.com/beeper/ai-bridge/pkg/shared/citations"
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

	uiFinished              bool
	uiTextID                string
	uiReasoningID           string
	uiToolStarted           map[string]bool
	uiSourceURLSeen         map[string]bool
	uiToolCallIDByApproval  map[string]string
	uiToolApprovalRequested map[string]bool
	uiToolNameByToolCallID  map[string]string
	uiToolTypeByToolCallID  map[string]matrixevents.ToolType
	uiToolOutputFinalized   map[string]bool

	codexToolOutputBuffers    map[string]*strings.Builder
	codexLatestDiff           string
	codexReasoningSummarySeen bool
	codexTimelineNotices      map[string]bool
}

func newStreamingState(ctx context.Context, meta *PortalMetadata, sourceEventID id.EventID, senderID string, roomID id.RoomID) *streamingState {
	_ = ctx
	_ = meta
	_ = senderID
	_ = roomID
	return &streamingState{
		turnID:                  NewTurnID(),
		startedAtMs:             nowMillis(),
		firstToken:              true,
		initialEventID:          sourceEventID,
		uiToolStarted:           make(map[string]bool),
		uiSourceURLSeen:         make(map[string]bool),
		uiToolCallIDByApproval:  make(map[string]string),
		uiToolApprovalRequested: make(map[string]bool),
		uiToolNameByToolCallID:  make(map[string]string),
		uiToolTypeByToolCallID:  make(map[string]matrixevents.ToolType),
		uiToolOutputFinalized:   make(map[string]bool),
		codexTimelineNotices:    make(map[string]bool),
		codexToolOutputBuffers:  make(map[string]*strings.Builder),
	}
}
