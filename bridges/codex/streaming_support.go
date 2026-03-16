package codex

import (
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/pkg/shared/citations"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

type streamingState struct {
	turnID           string
	currentModel     string
	agentID          string
	startedAtMs      int64
	firstTokenAtMs   int64
	completedAtMs    int64
	promptTokens     int64
	completionTokens int64
	reasoningTokens  int64
	totalTokens      int64
	accumulated      strings.Builder
	reasoning        strings.Builder
	toolCalls        []ToolCallMetadata
	sourceCitations  []citations.SourceCitation
	sourceDocuments  []citations.SourceDocument
	generatedFiles   []citations.GeneratedFilePart
	initialEventID   id.EventID
	networkMessageID networkid.MessageID
	firstToken       bool

	turn *bridgesdk.Turn

	codexToolOutputBuffers    map[string]*strings.Builder
	codexLatestDiff           string
	codexReasoningSummarySeen bool
	codexTimelineNotices      map[string]bool
}

func (s *streamingState) recordFirstToken() {
	if s == nil || !s.firstToken {
		return
	}
	s.firstToken = false
	s.firstTokenAtMs = time.Now().UnixMilli()
}

func newStreamingState(sourceEventID id.EventID) *streamingState {
	turnID := agentremote.NewTurnID()
	return &streamingState{
		turnID:                 turnID,
		startedAtMs:            time.Now().UnixMilli(),
		firstToken:             true,
		initialEventID:         sourceEventID,
		codexTimelineNotices:   make(map[string]bool),
		codexToolOutputBuffers: make(map[string]*strings.Builder),
	}
}
