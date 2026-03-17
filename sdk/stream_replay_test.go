package sdk

import (
	"testing"

	"github.com/beeper/agentremote/pkg/shared/citations"
	"github.com/beeper/agentremote/pkg/shared/streamui"
)

func TestUIStateReplayerReplaysCompletedContent(t *testing.T) {
	state := &streamui.UIState{TurnID: "turn-1"}
	replayer := NewUIStateReplayer(state)

	replayer.Start(map[string]any{"agent_id": "agent-1"})
	replayer.StepStart()
	replayer.Text("text-1", "hello")
	replayer.Reasoning("reasoning-1", "thinking")
	replayer.ToolInput("call-1", "bash", map[string]any{"cmd": "pwd"}, false)
	replayer.ApprovalRequest("approval-1", "call-1")
	replayer.ToolOutput("call-1", map[string]any{"stdout": "/tmp"}, false)
	replayer.Artifact(
		"source-1",
		citations.SourceCitation{URL: "https://example.com/out.txt"},
		citations.SourceDocument{ID: "doc-1", Title: "out.txt", Filename: "out.txt"},
		"text/plain",
	)
	replayer.StepFinish()
	replayer.Finish("", map[string]any{"finish_reason": "stop"})

	ui := streamui.SnapshotUIMessage(state)
	if ui == nil {
		t.Fatal("expected ui message")
	}
	metadata, _ := ui["metadata"].(map[string]any)
	if metadata["agent_id"] != "agent-1" {
		t.Fatalf("expected agent metadata, got %#v", metadata)
	}
	if metadata["finish_reason"] != "stop" {
		t.Fatalf("expected finish metadata to include stop, got %#v", metadata["finish_reason"])
	}
	td, ok := TurnDataFromUIMessage(ui)
	if !ok {
		t.Fatalf("expected turn data from ui, got %#v", ui)
	}
	parts := td.Parts
	if len(parts) != 7 {
		t.Fatalf("expected 7 parts, got %#v", parts)
	}
	if parts[0].Type != "step-start" {
		t.Fatalf("expected first part to be step-start, got %#v", parts[0])
	}
	if parts[1].Type != "text" || parts[1].Text != "hello" {
		t.Fatalf("expected replayed text part, got %#v", parts[1])
	}
	if parts[2].Type != "reasoning" || parts[2].Text != "thinking" {
		t.Fatalf("expected replayed reasoning part, got %#v", parts[2])
	}
	if parts[3].Type != "tool" || parts[3].State != "output-available" {
		t.Fatalf("expected replayed tool part, got %#v", parts[3])
	}
	if parts[4].Type != "file" {
		t.Fatalf("expected replayed file part, got %#v", parts[4])
	}
	if parts[5].Type != "source-url" {
		t.Fatalf("expected replayed source url part, got %#v", parts[5])
	}
	if parts[6].Type != "source-document" {
		t.Fatalf("expected replayed document part, got %#v", parts[6])
	}
}

func TestUIStateReplayerToolInputTextAndDefaults(t *testing.T) {
	state := &streamui.UIState{TurnID: "turn-2"}
	replayer := NewUIStateReplayer(state)

	replayer.Start(nil)
	replayer.ToolInputText("call-1", "bash", "{\"cmd\":\"pwd\"}", false)
	replayer.ToolOutputError("call-1", "boom", false)
	replayer.Finish("", map[string]any{"finish_reason": "stop"})

	ui := streamui.SnapshotUIMessage(state)
	metadata, _ := ui["metadata"].(map[string]any)
	if metadata["finish_reason"] != "stop" {
		t.Fatalf("expected stop finish reason metadata, got %#v", metadata["finish_reason"])
	}
	td, ok := TurnDataFromUIMessage(ui)
	if !ok {
		t.Fatalf("expected turn data from ui, got %#v", ui)
	}
	parts := td.Parts
	if len(parts) != 1 {
		t.Fatalf("expected 1 tool part, got %#v", parts)
	}
	if parts[0].State != "output-error" {
		t.Fatalf("expected tool output-error state, got %#v", parts[0])
	}
}
