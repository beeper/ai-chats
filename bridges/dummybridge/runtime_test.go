package dummybridge

import (
	"context"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"

	bridgesdk "github.com/beeper/agentremote/sdk"
)

type testApprovalHandle struct {
	id         string
	toolCallID string
	approved   bool
	reason     string
}

func (h *testApprovalHandle) ID() string         { return h.id }
func (h *testApprovalHandle) ToolCallID() string { return h.toolCallID }
func (h *testApprovalHandle) Wait(context.Context) (bridgesdk.ToolApprovalResponse, error) {
	return bridgesdk.ToolApprovalResponse{
		Approved: h.approved,
		Reason:   h.reason,
	}, nil
}

func testRunner() demoRunner {
	return demoRunner{
		runtime: demoRuntime{
			now: time.Now,
			sleep: func(context.Context, time.Duration) error {
				return nil
			},
		},
	}
}

func newTestTurn() *bridgesdk.Turn {
	cfg := &bridgesdk.Config{
		ProviderIdentity: bridgesdk.ProviderIdentity{IDPrefix: "dummybridge", StatusNetwork: "dummybridge"},
	}
	conv := bridgesdk.NewConversation(context.Background(), nil, nil, bridgev2.EventSender{}, cfg, nil)
	return conv.StartTurn(context.Background(), dummySDKAgent(), nil)
}

func findPartByType(parts []map[string]any, partType string) map[string]any {
	for _, part := range parts {
		if part["type"] == partType {
			return part
		}
	}
	return nil
}

func findToolPart(parts []map[string]any, toolName string) map[string]any {
	for _, part := range parts {
		if part["type"] != "tool" && part["type"] != "dynamic-tool" {
			continue
		}
		if part["toolName"] == toolName {
			return part
		}
		if input, ok := part["input"].(map[string]any); ok && input["tool"] == toolName {
			return part
		}
	}
	return nil
}

func TestParseToolsCommandRejectsConflictingFinalTags(t *testing.T) {
	_, err := parseCommand("stream-tools 100 shell#fail#approval")
	if err == nil {
		t.Fatal("expected parse error for conflicting tool tags")
	}
}

func TestParseCommandRecognizesHelpAliases(t *testing.T) {
	tests := []string{"help", "/help", "!help", "dummybridge help"}
	for _, input := range tests {
		cmd, err := parseCommand(input)
		if err != nil {
			t.Fatalf("parseCommand(%q) returned error: %v", input, err)
		}
		if cmd == nil || cmd.Name != "help" {
			t.Fatalf("expected help command for %q, got %#v", input, cmd)
		}
	}
}

func TestHelpTextIncludesCommandGuide(t *testing.T) {
	help := helpText()
	for _, snippet := range []string{"stream-lorem", "stream-tools", "stream-random", "stream-chaos", "approval-tagged tools"} {
		if !strings.Contains(help, snippet) {
			t.Fatalf("expected help text to include %q, got %q", snippet, help)
		}
	}
}

func TestParseLoremCommandRejectsConflictingTerminalOptions(t *testing.T) {
	_, err := parseCommand("stream-lorem 100 --finish=length --abort")
	if err == nil {
		t.Fatal("expected conflicting terminal option error")
	}
}

func TestParseRandomCommandRejectsUnknownProfile(t *testing.T) {
	_, err := parseCommand("stream-random 5 --profile=nope")
	if err == nil {
		t.Fatal("expected invalid profile error")
	}
}

func TestRunLoremEmitsArtifactsAndPersistentData(t *testing.T) {
	turn := newTestTurn()
	cmd := loremCommand{
		Chars: 120,
		Options: commonCommandOptions{
			ReasoningChars:    40,
			Steps:             2,
			Sources:           1,
			Documents:         1,
			Files:             1,
			Meta:              true,
			DataName:          "demo",
			DataTransientName: "demo-transient",
			DelayMin:          0,
			DelayMax:          0,
			ChunkMin:          12,
			ChunkMax:          12,
			FinishReason:      "length",
			Seed:              7,
			SeedSet:           true,
		},
	}
	if err := testRunner().runLorem(context.Background(), turn, cmd, zerolog.Nop()); err != nil {
		t.Fatalf("runLorem returned error: %v", err)
	}
	parts := snapshotParts(turn)
	if findPartByType(parts, "step-start") == nil {
		t.Fatal("expected step-start part")
	}
	if findPartByType(parts, "reasoning") == nil {
		t.Fatal("expected reasoning part")
	}
	if findPartByType(parts, "text") == nil {
		t.Fatal("expected text part")
	}
	if findPartByType(parts, "source-url") == nil {
		t.Fatal("expected source-url part")
	}
	if findPartByType(parts, "source-document") == nil {
		t.Fatal("expected source-document part")
	}
	if findPartByType(parts, "file") == nil {
		t.Fatal("expected file part")
	}
	if findPartByType(parts, "data-demo") == nil {
		t.Fatal("expected persistent data part")
	}
	if findPartByType(parts, "data-demo-transient") != nil {
		t.Fatal("did not expect transient data part to persist in the snapshot")
	}
}

func TestRunToolsApprovalDeniedProducesDeniedToolState(t *testing.T) {
	turn := newTestTurn()
	turn.Approvals().SetHandler(func(_ context.Context, _ *bridgesdk.Turn, req bridgesdk.ApprovalRequest) bridgesdk.ApprovalHandle {
		return &testApprovalHandle{
			id:         "approval-1",
			toolCallID: req.ToolCallID,
			approved:   false,
			reason:     "deny",
		}
	})
	cmd := toolsCommand{
		Chars: 30,
		Tools: []toolSpec{{
			Name:          "shell",
			Approval:      true,
			SequenceIndex: 1,
		}},
		Options: commonCommandOptions{
			DelayMin:     0,
			DelayMax:     0,
			ChunkMin:     10,
			ChunkMax:     10,
			FinishReason: "stop",
		},
	}
	if err := testRunner().runTools(context.Background(), turn, cmd, zerolog.Nop()); err != nil {
		t.Fatalf("runTools returned error: %v", err)
	}
	part := findToolPart(snapshotParts(turn), "shell")
	if part == nil {
		t.Fatalf("expected tool part for shell, got %#v", snapshotParts(turn))
	}
	if state := part["state"]; state != "output-denied" {
		t.Fatalf("expected denied tool state, got %#v", state)
	}
}

func TestRunRandomUsesSingleTurnAndFinishes(t *testing.T) {
	turn := newTestTurn()
	cmd := randomCommand{
		Duration:   2 * time.Second,
		Actions:    4,
		Profile:    "balanced",
		DelayMin:   0,
		DelayMax:   0,
		Seed:       99,
		SeedSet:    true,
		AllowAbort: false,
		AllowError: false,
	}
	if err := testRunner().runRandom(context.Background(), turn, cmd, zerolog.Nop()); err != nil {
		t.Fatalf("runRandom returned error: %v", err)
	}
	if !turn.UIState().UIFinished {
		t.Fatal("expected random run to finish the turn")
	}
	if len(snapshotParts(turn)) == 0 {
		t.Fatal("expected random run to emit parts")
	}
}

func TestBuildLoremTextProducesCleanSentenceLikeOutput(t *testing.T) {
	text := buildLoremText(140, rand.New(rand.NewSource(7)))
	if text == "" {
		t.Fatal("expected lorem text")
	}
	if first := text[0]; first < 'A' || first > 'Z' {
		t.Fatalf("expected text to start with an uppercase letter, got %q", text)
	}
	if strings.Contains(text, "  ") {
		t.Fatalf("expected no repeated spaces, got %q", text)
	}
	if last := text[len(text)-1]; (last >= 'a' && last <= 'z') || (last >= 'A' && last <= 'Z') {
		t.Fatalf("expected text to end cleanly, got %q", text)
	}
}

func TestBuildLoremTextVariesAcrossCalls(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	first := buildLoremText(160, rng)
	second := buildLoremText(160, rng)
	if first == second {
		t.Fatalf("expected distinct lorem passages, got %q", first)
	}
}

func TestRunLoremErrorSetsTerminalErrorState(t *testing.T) {
	turn := newTestTurn()
	cmd := loremCommand{
		Chars: 40,
		Options: commonCommandOptions{
			DelayMin: 0,
			DelayMax: 0,
			ChunkMin: 10,
			ChunkMax: 10,
			Error:    true,
			Seed:     1,
			SeedSet:  true,
		},
	}
	if err := testRunner().runLorem(context.Background(), turn, cmd, zerolog.Nop()); err != nil {
		t.Fatalf("runLorem returned error: %v", err)
	}
	ui := turn.UIState().UIMessage
	metadata, _ := ui["metadata"].(map[string]any)
	terminal, _ := metadata["beeper_terminal_state"].(map[string]any)
	if terminal["type"] != "error" {
		t.Fatalf("expected error terminal state, got %#v", terminal)
	}
}

func TestRunLoremAbortSetsTerminalAbortState(t *testing.T) {
	turn := newTestTurn()
	cmd := loremCommand{
		Chars: 40,
		Options: commonCommandOptions{
			DelayMin: 0,
			DelayMax: 0,
			ChunkMin: 10,
			ChunkMax: 10,
			Abort:    true,
			Seed:     2,
			SeedSet:  true,
		},
	}
	if err := testRunner().runLorem(context.Background(), turn, cmd, zerolog.Nop()); err != nil {
		t.Fatalf("runLorem returned error: %v", err)
	}
	ui := turn.UIState().UIMessage
	metadata, _ := ui["metadata"].(map[string]any)
	terminal, _ := metadata["beeper_terminal_state"].(map[string]any)
	if terminal["type"] != "abort" {
		t.Fatalf("expected abort terminal state, got %#v", terminal)
	}
}
