package ai

import (
	"testing"

	"github.com/beeper/agentremote/bridges/ai/msgconv"
	"github.com/beeper/agentremote/pkg/shared/citations"
)

func TestMapFinishReason(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{name: "tool_calls", input: "tool_calls", expect: "tool-calls"},
		{name: "tool_use", input: "tool_use", expect: "tool-calls"},
		{name: "tool-use", input: "tool-use", expect: "tool-calls"},
		{name: "toolUse", input: "toolUse", expect: "tool-calls"},
		{name: "end_turn", input: "end_turn", expect: "stop"},
		{name: "end-turn", input: "end-turn", expect: "stop"},
		{name: "stop", input: "stop", expect: "stop"},
		{name: "other", input: "weird", expect: "other"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := msgconv.MapFinishReason(tc.input)
			if got != tc.expect {
				t.Fatalf("msgconv.MapFinishReason(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}

func TestShouldContinueChatToolLoop(t *testing.T) {
	tests := []struct {
		name       string
		reason     string
		toolCalls  int
		shouldLoop bool
	}{
		{name: "no_tool_calls", reason: "tool_calls", toolCalls: 0, shouldLoop: false},
		{name: "tool_calls", reason: "tool_calls", toolCalls: 1, shouldLoop: true},
		{name: "tool_use", reason: "tool_use", toolCalls: 1, shouldLoop: true},
		{name: "tool-use", reason: "tool-use", toolCalls: 1, shouldLoop: true},
		{name: "toolUse", reason: "toolUse", toolCalls: 1, shouldLoop: true},
		{name: "empty_reason", reason: "", toolCalls: 1, shouldLoop: true},
		{name: "stop_reason_with_tool_calls", reason: "stop", toolCalls: 1, shouldLoop: true},
		{name: "length_reason_with_tool_calls", reason: "length", toolCalls: 1, shouldLoop: true},
		{name: "error_reason", reason: "error", toolCalls: 1, shouldLoop: false},
		{name: "cancelled_reason", reason: "cancelled", toolCalls: 1, shouldLoop: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldContinueChatToolLoop(tc.reason, tc.toolCalls)
			if got != tc.shouldLoop {
				t.Fatalf(
					"shouldContinueChatToolLoop(%q, %d) = %v, want %v",
					tc.reason,
					tc.toolCalls,
					got,
					tc.shouldLoop,
				)
			}
		})
	}
}

func TestBuildStreamUIMessage_IncludesSourceAndFileParts(t *testing.T) {
	oc := &AIClient{}
	state := testStreamingState("turn-1")
	state.sourceCitations = []citations.SourceCitation{{
		URL:   "https://example.com",
		Title: "Example",
	}}
	state.sourceDocuments = []citations.SourceDocument{{
		ID:        "doc-1",
		Title:     "Doc",
		Filename:  "doc.txt",
		MediaType: "text/plain",
	}}
	state.generatedFiles = []citations.GeneratedFilePart{{
		URL:       "mxc://example/file",
		MediaType: "image/png",
	}}

	ui := oc.buildStreamUIMessage(state, modelModeTestMeta("openai/gpt-4.1"), nil)
	if ui == nil {
		t.Fatalf("expected canonical message")
	}
	partsRaw, ok := ui["parts"].([]map[string]any)
	if !ok {
		partsAny, okAny := ui["parts"].([]any)
		if !okAny {
			t.Fatalf("expected parts array, got %T", ui["parts"])
		}
		partsRaw = make([]map[string]any, 0, len(partsAny))
		for _, partAny := range partsAny {
			part, okPart := partAny.(map[string]any)
			if okPart {
				partsRaw = append(partsRaw, part)
			}
		}
	}

	foundSourceURL := false
	foundSourceDocument := false
	foundFile := false
	for _, part := range partsRaw {
		switch part["type"] {
		case "source-url":
			foundSourceURL = true
		case "source-document":
			foundSourceDocument = true
		case "file":
			foundFile = true
		}
	}
	if !foundSourceURL || !foundSourceDocument || !foundFile {
		t.Fatalf("missing expected part types (source-url=%v source-document=%v file=%v)", foundSourceURL, foundSourceDocument, foundFile)
	}
}
