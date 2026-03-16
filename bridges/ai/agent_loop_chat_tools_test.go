package ai

import (
	"strings"
	"testing"
)

func TestExecuteChatToolCallsSequentially_StopsAfterSteeringArrives(t *testing.T) {
	activeTools := newStreamToolRegistry()
	first, _ := activeTools.Upsert("tool-1", func(string) *activeToolCall {
		var input strings.Builder
		input.WriteString(`{"first":true}`)
		return &activeToolCall{
			callID:   "call_1",
			toolName: "Read",
			input:    input,
		}
	})
	second, _ := activeTools.Upsert("tool-2", func(string) *activeToolCall {
		var input strings.Builder
		input.WriteString(`{"second":true}`)
		return &activeToolCall{
			callID:   "call_2",
			toolName: "Edit",
			input:    input,
		}
	})
	if first == nil || second == nil {
		t.Fatal("expected active tools to be created")
	}

	var executed []string
	var steeringChecks int
	toolCallParams, steeringMessages := executeChatToolCallsSequentially(
		activeTools.SortedKeys(),
		activeTools,
		func(tool *activeToolCall, toolName, argsJSON string) {
			executed = append(executed, toolName+":"+argsJSON)
		},
		func() []string {
			steeringChecks++
			if len(executed) == 1 {
				return []string{"interrupt with steering"}
			}
			return nil
		},
	)

	if len(toolCallParams) != 1 {
		t.Fatalf("expected only one executed tool call param, got %d", len(toolCallParams))
	}
	if len(executed) != 1 || executed[0] != `Read:{"first":true}` {
		t.Fatalf("unexpected executed tools: %#v", executed)
	}
	if len(steeringMessages) != 1 || steeringMessages[0] != "interrupt with steering" {
		t.Fatalf("unexpected steering messages: %#v", steeringMessages)
	}
	if steeringChecks != 1 {
		t.Fatalf("expected one steering check after first tool execution, got %d", steeringChecks)
	}
}
