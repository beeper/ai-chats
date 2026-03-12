package agentremote

import (
	"strings"
	"sync"
	"time"

	"github.com/beeper/agentremote/turns"
)

// AgentMessageRole is the canonical internal role for Pi-style turn messages.
type AgentMessageRole string

const (
	RoleAssistant    AgentMessageRole = "assistant"
	RoleUser         AgentMessageRole = "user"
	RoleToolResult   AgentMessageRole = "tool_result"
	RoleNotification AgentMessageRole = "notification"
	RoleProgress     AgentMessageRole = "progress"
)

// AgentMessage is the internal turn-native message representation used by the
// public agentremote runtime. Matrix/AI SDK payloads are derived projections.
type AgentMessage struct {
	ID        string
	Role      AgentMessageRole
	Text      string
	Metadata  map[string]any
	Timestamp int64
}

// ToolExecutionState tracks the lifecycle of a tool call within a turn.
type ToolExecutionState struct {
	CallID        string
	ToolName      string
	Status        string
	Args          map[string]any
	Result        map[string]any
	PartialResult map[string]any
	IsError       bool
}

// TurnEventType enumerates the canonical internal turn lifecycle.
type TurnEventType string

const (
	TurnEventStart                 TurnEventType = "turn_start"
	TurnEventMessageStart          TurnEventType = "message_start"
	TurnEventMessageUpdate         TurnEventType = "message_update"
	TurnEventMessageEnd            TurnEventType = "message_end"
	TurnEventToolExecutionStart    TurnEventType = "tool_execution_start"
	TurnEventToolExecutionUpdate   TurnEventType = "tool_execution_update"
	TurnEventToolExecutionApproval TurnEventType = "tool_execution_approval_required"
	TurnEventToolExecutionEnd      TurnEventType = "tool_execution_end"
	TurnEventEnd                   TurnEventType = "turn_end"
	TurnEventAbort                 TurnEventType = "turn_abort"
	TurnEventError                 TurnEventType = "turn_error"
)

// TurnEvent is the canonical internal event emitted by a managed turn.
type TurnEvent struct {
	Type          TurnEventType
	Message       *AgentMessage
	ToolExecution *ToolExecutionState
	Error         string
	Metadata      map[string]any
	Timestamp     int64
}

// TurnSnapshot is the durable in-memory representation of a turn as events are
// applied. Bridges can project this state into Matrix/Beeper payloads.
type TurnSnapshot struct {
	TurnID           string
	AgentID          string
	VisibleText      string
	ReasoningText    string
	Messages         []AgentMessage
	ToolExecutions   []ToolExecutionState
	Events           []TurnEvent
	StartedAtMs      int64
	FirstTokenAtMs   int64
	CompletedAtMs    int64
	FinishReason     string
	LastError        string
	NetworkMessageID string
	TargetEventID    string
}

// TurnManager tracks active turns for a runtime.
type TurnManager struct {
	runtime *Runtime
	mu      sync.Mutex
	turns   map[string]*Turn
}

// TurnOptions configures a new managed turn.
type TurnOptions struct {
	ID      string
	AgentID string
}

// Turn is the public managed turn handle. It owns the Pi-style snapshot and can
// optionally attach to a streaming transport session.
type Turn struct {
	runtime *Runtime
	mu      sync.Mutex

	ID      string
	AgentID string

	Snapshot TurnSnapshot
	Session  *turns.StreamSession
}

func NewTurnManager(runtime *Runtime) *TurnManager {
	return &TurnManager{
		runtime: runtime,
		turns:   make(map[string]*Turn),
	}
}

func (m *TurnManager) StartTurn(opts TurnOptions) *Turn {
	if m == nil {
		return nil
	}
	turnID := strings.TrimSpace(opts.ID)
	if turnID == "" {
		return nil
	}
	agentID := strings.TrimSpace(opts.AgentID)
	if agentID == "" && m.runtime != nil {
		agentID = m.runtime.AgentID
	}
	turn := &Turn{
		runtime: m.runtime,
		ID:      turnID,
		AgentID: agentID,
		Snapshot: TurnSnapshot{
			TurnID:      turnID,
			AgentID:     agentID,
			StartedAtMs: time.Now().UnixMilli(),
		},
	}
	turn.ApplyEvent(TurnEvent{Type: TurnEventStart})
	m.mu.Lock()
	m.turns[turnID] = turn
	m.mu.Unlock()
	return turn
}

func (m *TurnManager) Get(turnID string) *Turn {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.turns[strings.TrimSpace(turnID)]
}

func (m *TurnManager) End(turnID string, reason turns.EndReason) {
	if m == nil {
		return
	}
	m.mu.Lock()
	turn := m.turns[strings.TrimSpace(turnID)]
	delete(m.turns, strings.TrimSpace(turnID))
	m.mu.Unlock()
	if turn == nil {
		return
	}
	if turn.Session != nil {
		turn.Session.End(nil, reason)
	}
	turn.mu.Lock()
	if turn.Snapshot.CompletedAtMs == 0 {
		turn.Snapshot.CompletedAtMs = time.Now().UnixMilli()
	}
	if turn.Snapshot.FinishReason == "" {
		turn.Snapshot.FinishReason = string(reason)
	}
	turn.mu.Unlock()
}

func (t *Turn) AttachSession(session *turns.StreamSession) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.Session = session
	t.mu.Unlock()
}

func (t *Turn) ApplyEvent(evt TurnEvent) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if evt.Timestamp == 0 {
		evt.Timestamp = time.Now().UnixMilli()
	}
	t.Snapshot.Events = append(t.Snapshot.Events, evt)
	switch evt.Type {
	case TurnEventMessageStart, TurnEventMessageUpdate, TurnEventMessageEnd:
		if evt.Message != nil {
			msg := *evt.Message
			if msg.Timestamp == 0 {
				msg.Timestamp = evt.Timestamp
			}
			t.Snapshot.Messages = append(t.Snapshot.Messages, msg)
			if msg.Role == RoleAssistant {
				if msg.Text != "" {
					t.Snapshot.VisibleText += msg.Text
					if t.Snapshot.FirstTokenAtMs == 0 {
						t.Snapshot.FirstTokenAtMs = evt.Timestamp
					}
				}
			}
		}
	case TurnEventToolExecutionStart, TurnEventToolExecutionUpdate, TurnEventToolExecutionApproval, TurnEventToolExecutionEnd:
		if evt.ToolExecution != nil {
			t.Snapshot.ToolExecutions = append(t.Snapshot.ToolExecutions, *evt.ToolExecution)
		}
	case TurnEventAbort:
		t.Snapshot.FinishReason = "aborted"
		t.Snapshot.CompletedAtMs = evt.Timestamp
	case TurnEventError:
		t.Snapshot.FinishReason = "error"
		t.Snapshot.LastError = strings.TrimSpace(evt.Error)
		t.Snapshot.CompletedAtMs = evt.Timestamp
	case TurnEventEnd:
		if reason := strings.TrimSpace(stringValue(evt.Metadata, "finish_reason")); reason != "" {
			t.Snapshot.FinishReason = reason
		}
		t.Snapshot.CompletedAtMs = evt.Timestamp
	}
}

func (t *Turn) SnapshotCopy() TurnSnapshot {
	if t == nil {
		return TurnSnapshot{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	cp := t.Snapshot
	cp.Messages = append([]AgentMessage(nil), t.Snapshot.Messages...)
	cp.ToolExecutions = append([]ToolExecutionState(nil), t.Snapshot.ToolExecutions...)
	cp.Events = append([]TurnEvent(nil), t.Snapshot.Events...)
	return cp
}

func stringValue(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	raw, _ := values[key].(string)
	return strings.TrimSpace(raw)
}
