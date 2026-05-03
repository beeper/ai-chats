package matrixevents

import (
	"fmt"
	"strings"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Event types shared across bridge/bot/modules.
//
// Keep these values stable: clients may rely on them for rendering and behavior.
var (
	StreamEventMessageType = event.Type{Type: "com.beeper.llm", Class: event.EphemeralEventType}

	AIRoomInfoEventType = event.Type{Type: "com.beeper.ai.info", Class: event.StateEventType}
)

// Relation types.
const (
	RelReplace   = "m.replace"
	RelReference = "m.reference"
	RelThread    = "m.thread"
)

// Content field keys.
const BeeperAIKey = "com.beeper.ai"

// ToolStatus represents the state of a tool call.
type ToolStatus string

const (
	ToolStatusPending   ToolStatus = "pending"
	ToolStatusRunning   ToolStatus = "running"
	ToolStatusCompleted ToolStatus = "completed"
	ToolStatusFailed    ToolStatus = "failed"
	ToolStatusTimeout   ToolStatus = "timeout"
	ToolStatusCancelled ToolStatus = "cancelled"
)

// ResultStatus represents the status of a tool result.
type ResultStatus string

const (
	ResultStatusSuccess ResultStatus = "success"
	ResultStatusError   ResultStatus = "error"
	ResultStatusPartial ResultStatus = "partial"
	ResultStatusDenied  ResultStatus = "denied"
)

// ToolType identifies the category of tool.
type ToolType string

const (
	ToolTypeBuiltin  ToolType = "builtin"
	ToolTypeProvider ToolType = "provider"
	ToolTypeFunction ToolType = "function"
)

type StreamEventOpts struct {
	RelatesToEventID string
	AgentID          string
}

// BuildStreamEventEnvelope builds the stable envelope for com.beeper.llm delta payloads.
func BuildStreamEventEnvelope(turnID string, seq int, part map[string]any, opts StreamEventOpts) (map[string]any, error) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return nil, fmt.Errorf("stream event envelope: missing turn_id")
	}
	if seq <= 0 {
		return nil, fmt.Errorf("stream event envelope: seq must be > 0 (got %d)", seq)
	}
	if part == nil {
		return nil, fmt.Errorf("stream event envelope: missing part")
	}

	content := map[string]any{
		"turn_id": turnID,
		"seq":     seq,
		"part":    part,
	}

	target := strings.TrimSpace(opts.RelatesToEventID)
	if target == "" {
		return nil, fmt.Errorf("stream event envelope: missing m.relates_to event_id")
	}
	content["m.relates_to"] = &event.RelatesTo{
		Type:    event.RelReference,
		EventID: id.EventID(target),
	}
	if agentID := strings.TrimSpace(opts.AgentID); agentID != "" {
		content["agent_id"] = agentID
	}

	return content, nil
}

func BuildStreamEventTxnID(turnID string, seq int) string {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return ""
	}
	return fmt.Sprintf("ai_stream_%s_%d", turnID, seq)
}
