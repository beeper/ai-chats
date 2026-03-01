package connector

import (
	"github.com/beeper/ai-bridge/pkg/shared/citations"
)

func collectToolOutputCitations(state *streamingState, toolName, output string) {
	if state == nil {
		return
	}
	extracted := extractWebSearchCitationsFromToolOutput(toolName, output)
	if len(extracted) == 0 {
		return
	}
	state.sourceCitations = citations.MergeSourceCitations(state.sourceCitations, extracted)
}
