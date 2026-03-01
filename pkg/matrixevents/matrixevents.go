package matrixevents

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Event types shared across bridge/bot/modules.
//
// Keep these values stable: clients may rely on them for rendering and behavior.
var (
	ToolCallEventType   = event.Type{Type: "com.beeper.ai.tool_call", Class: event.MessageEventType}
	ToolResultEventType = event.Type{Type: "com.beeper.ai.tool_result", Class: event.MessageEventType}

	StreamEventMessageType = event.Type{Type: "com.beeper.ai.stream_event", Class: event.EphemeralEventType}

	CompactionStatusEventType = event.Type{Type: "com.beeper.ai.compaction_status", Class: event.MessageEventType}

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
)

// Content field keys.
const (
	BeeperAIKey           = "com.beeper.ai"
	BeeperAIToolCallKey   = "com.beeper.ai.tool_call"
	BeeperAIToolResultKey = "com.beeper.ai.tool_result"
)

// ToolStatus represents the state of a tool call.
type ToolStatus string

const (
	ToolStatusPending          ToolStatus = "pending"
	ToolStatusRunning          ToolStatus = "running"
	ToolStatusCompleted        ToolStatus = "completed"
	ToolStatusFailed           ToolStatus = "failed"
	ToolStatusTimeout          ToolStatus = "timeout"
	ToolStatusCancelled        ToolStatus = "cancelled"
	ToolStatusApprovalRequired ToolStatus = "approval_required"
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
	ToolTypeMCP      ToolType = "mcp"
)

// MatrixEphemeralSender is the subset of a Matrix client/intent needed to send ephemeral events.
type MatrixEphemeralSender interface {
	SendEphemeralEvent(ctx context.Context, roomID id.RoomID, eventType event.Type, content *event.Content, txnID string) (*mautrix.RespSendEvent, error)
}

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
