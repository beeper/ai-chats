package opencode

import (
	"encoding/json"
)

// Timestamp represents millisecond timestamps returned by the OpenCode API.
type Timestamp int64

// UnmarshalJSON accepts either integer or floating-point JSON numbers.
func (t *Timestamp) UnmarshalJSON(data []byte) error {
	var num json.Number
	if err := json.Unmarshal(data, &num); err != nil {
		return err
	}
	if value, err := num.Int64(); err == nil {
		*t = Timestamp(value)
		return nil
	}
	value, err := num.Float64()
	if err != nil {
		return err
	}
	*t = Timestamp(int64(value))
	return nil
}

// Session represents an OpenCode session summary.
type Session struct {
	ID        string      `json:"id"`
	Slug      string      `json:"slug"`
	ProjectID string      `json:"projectID"`
	Directory string      `json:"directory"`
	ParentID  string      `json:"parentID,omitempty"`
	Title     string      `json:"title"`
	Version   string      `json:"version"`
	Time      SessionTime `json:"time"`
}

// SessionTime holds session timing metadata.
type SessionTime struct {
	Created Timestamp `json:"created"`
	Updated Timestamp `json:"updated"`
}

// Message represents the info block for a session message.
type Message struct {
	ID        string      `json:"id"`
	SessionID string      `json:"sessionID"`
	Role      string      `json:"role"`
	Agent     string      `json:"agent,omitempty"`
	Time      MessageTime `json:"time"`
}

// MessageTime holds timing info for a message.
type MessageTime struct {
	Created   Timestamp `json:"created"`
	Completed Timestamp `json:"completed,omitempty"`
}

// Part represents a message part. Only a subset of fields is used by the bridge.
type Part struct {
	ID          string          `json:"id"`
	SessionID   string          `json:"sessionID,omitempty"`
	MessageID   string          `json:"messageID,omitempty"`
	Type        string          `json:"type"`
	Text        string          `json:"text,omitempty"`
	Filename    string          `json:"filename,omitempty"`
	URL         string          `json:"url,omitempty"`
	Mime        string          `json:"mime,omitempty"`
	Name        string          `json:"name,omitempty"`
	Prompt      string          `json:"prompt,omitempty"`
	Description string          `json:"description,omitempty"`
	Agent       string          `json:"agent,omitempty"`
	Model       *ModelRef       `json:"model,omitempty"`
	Command     string          `json:"command,omitempty"`
	CallID      string          `json:"callID,omitempty"`
	Tool        string          `json:"tool,omitempty"`
	State       *ToolState      `json:"state,omitempty"`
	Snapshot    string          `json:"snapshot,omitempty"`
	Hash        string          `json:"hash,omitempty"`
	Files       []string        `json:"files,omitempty"`
	Reason      string          `json:"reason,omitempty"`
	Cost        float64         `json:"cost,omitempty"`
	Tokens      *TokenUsage     `json:"tokens,omitempty"`
	Attempt     int             `json:"attempt,omitempty"`
	Auto        bool            `json:"auto,omitempty"`
	Time        *PartTime       `json:"time,omitempty"`
	Metadata    map[string]any  `json:"metadata,omitempty"`
	Error       json.RawMessage `json:"error,omitempty"`
	Source      json.RawMessage `json:"source,omitempty"`
	Extra       map[string]any  `json:"extra,omitempty"`
}

// PartTime represents part timing metadata.
type PartTime struct {
	Start Timestamp `json:"start"`
	End   Timestamp `json:"end,omitempty"`
}

// ModelRef identifies a provider/model pair.
type ModelRef struct {
	ProviderID string `json:"providerID,omitempty"`
	ModelID    string `json:"modelID,omitempty"`
}

// ToolStateTime captures tool state timing.
type ToolStateTime struct {
	Start     Timestamp `json:"start,omitempty"`
	End       Timestamp `json:"end,omitempty"`
	Compacted Timestamp `json:"compacted,omitempty"`
}

// ToolState captures tool execution state.
type ToolState struct {
	Status      string         `json:"status"`
	Input       map[string]any `json:"input,omitempty"`
	Raw         string         `json:"raw,omitempty"`
	Title       string         `json:"title,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Output      string         `json:"output,omitempty"`
	Error       string         `json:"error,omitempty"`
	Time        *ToolStateTime `json:"time,omitempty"`
	Attachments []Part         `json:"attachments,omitempty"`
}

// TokenCache represents cached token usage.
type TokenCache struct {
	Read  float64 `json:"read,omitempty"`
	Write float64 `json:"write,omitempty"`
}

// TokenUsage represents token usage.
type TokenUsage struct {
	Input     float64     `json:"input,omitempty"`
	Output    float64     `json:"output,omitempty"`
	Reasoning float64     `json:"reasoning,omitempty"`
	Cache     *TokenCache `json:"cache,omitempty"`
}

// PartInput is used to send parts to OpenCode.
type PartInput struct {
	ID          string    `json:"id,omitempty"`
	Type        string    `json:"type"`
	Text        string    `json:"text,omitempty"`
	Mime        string    `json:"mime,omitempty"`
	Filename    string    `json:"filename,omitempty"`
	URL         string    `json:"url,omitempty"`
	Name        string    `json:"name,omitempty"`
	Prompt      string    `json:"prompt,omitempty"`
	Description string    `json:"description,omitempty"`
	Agent       string    `json:"agent,omitempty"`
	Model       *ModelRef `json:"model,omitempty"`
	Command     string    `json:"command,omitempty"`
}

// MessageWithParts bundles a message info block with its parts.
type MessageWithParts struct {
	Info  Message `json:"info"`
	Parts []Part  `json:"parts"`
}

// Event represents a server-sent event from the OpenCode event stream.
type Event struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

// DecodeInfo decodes the info payload inside an event into the provided target.
func (e Event) DecodeInfo(target any) error {
	var wrapper struct {
		Info json.RawMessage `json:"info"`
	}
	if err := json.Unmarshal(e.Properties, &wrapper); err != nil {
		return err
	}
	if len(wrapper.Info) == 0 {
		return nil
	}
	return json.Unmarshal(wrapper.Info, target)
}
