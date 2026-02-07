package matrixevents

import (
	"errors"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/event"
)

// Event types shared across bridge/bot/modules.
//
// Keep these values stable: clients may rely on them for rendering and behavior.
var (
	AssistantTurnEventType = event.Type{Type: "com.beeper.ai.assistant_turn", Class: event.MessageEventType}
	ToolCallEventType      = event.Type{Type: "com.beeper.ai.tool_call", Class: event.MessageEventType}
	ToolResultEventType    = event.Type{Type: "com.beeper.ai.tool_result", Class: event.MessageEventType}
	AIErrorEventType       = event.Type{Type: "com.beeper.ai.error", Class: event.MessageEventType}
	TurnCancelledEventType = event.Type{Type: "com.beeper.ai.turn_cancelled", Class: event.MessageEventType}
	AgentHandoffEventType  = event.Type{Type: "com.beeper.ai.agent_handoff", Class: event.MessageEventType}
	StepBoundaryEventType  = event.Type{Type: "com.beeper.ai.step_boundary", Class: event.MessageEventType}

	StreamDeltaEventType   = event.Type{Type: "com.beeper.ai.stream_delta", Class: event.EphemeralEventType}
	StreamEventMessageType = event.Type{Type: "com.beeper.ai.stream_event", Class: event.EphemeralEventType}

	GenerationStatusEventType = event.Type{Type: "com.beeper.ai.generation_status", Class: event.MessageEventType}
	ToolProgressEventType     = event.Type{Type: "com.beeper.ai.tool_progress", Class: event.MessageEventType}
	CompactionStatusEventType = event.Type{Type: "com.beeper.ai.compaction_status", Class: event.MessageEventType}
	ApprovalRequestEventType  = event.Type{Type: "com.beeper.ai.approval_request", Class: event.MessageEventType}

	RoomCapabilitiesEventType  = event.Type{Type: "com.beeper.ai.room_capabilities", Class: event.StateEventType}
	RoomSettingsEventType      = event.Type{Type: "com.beeper.ai.room_settings", Class: event.StateEventType}
	ModelCapabilitiesEventType = event.Type{Type: "com.beeper.ai.model_capabilities", Class: event.StateEventType}
	AgentsEventType            = event.Type{Type: "com.beeper.ai.agents", Class: event.StateEventType}
)

// Relation types.
const (
	RelReplace   = "m.replace"
	RelReference = "m.reference"
	RelThread    = "m.thread"
	RelInReplyTo = "m.in_reply_to"
)

// Content field keys.
const (
	BeeperAIKey           = "com.beeper.ai"
	BeeperAIToolCallKey   = "com.beeper.ai.tool_call"
	BeeperAIToolResultKey = "com.beeper.ai.tool_result"
)

type StreamEventOpts struct {
	TargetEventID string
	AgentID       string
}

// BuildStreamEventEnvelope builds the stable envelope for com.beeper.ai.stream_event payloads.
func BuildStreamEventEnvelope(turnID string, seq int, part map[string]any, opts StreamEventOpts) (map[string]any, error) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return nil, errors.New("missing turn_id")
	}
	if seq <= 0 {
		return nil, errors.New("seq must be > 0")
	}
	if part == nil {
		return nil, errors.New("missing part")
	}

	content := map[string]any{
		"turn_id": turnID,
		"seq":     seq,
		"part":    part,
	}

	if target := strings.TrimSpace(opts.TargetEventID); target != "" {
		content["target_event"] = target
		content["m.relates_to"] = map[string]any{
			"rel_type": RelReference,
			"event_id": target,
		}
	}
	if agentID := strings.TrimSpace(opts.AgentID); agentID != "" {
		content["agent_id"] = agentID
	}

	return content, nil
}

func BuildStreamEventTxnID(turnID string, seq int) string {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return fmt.Sprintf("ai_stream_%d", seq)
	}
	return fmt.Sprintf("ai_stream_%s_%d", turnID, seq)
}
