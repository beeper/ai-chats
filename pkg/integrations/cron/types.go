package cron

type Schedule struct {
	Kind     string `json:"kind"`
	At       string `json:"at,omitempty"`
	EveryMs  int64  `json:"everyMs,omitempty"`
	AnchorMs *int64 `json:"anchorMs,omitempty"`
	Expr     string `json:"expr,omitempty"`
	TZ       string `json:"tz,omitempty"`
}

type DeliveryMode string

const (
	DeliveryNone     DeliveryMode = "none"
	DeliveryAnnounce DeliveryMode = "announce"
)

type Delivery struct {
	Mode       DeliveryMode `json:"mode"`
	Channel    string       `json:"channel,omitempty"`
	To         string       `json:"to,omitempty"`
	BestEffort *bool        `json:"bestEffort,omitempty"`
}

type DeliveryPatch struct {
	Mode       *DeliveryMode `json:"mode,omitempty"`
	Channel    *string       `json:"channel,omitempty"`
	To         *string       `json:"to,omitempty"`
	BestEffort *bool         `json:"bestEffort,omitempty"`
}

type Payload struct {
	Kind                string `json:"kind"`
	Message             string `json:"message,omitempty"`
	Model               string `json:"model,omitempty"`
	Thinking            string `json:"thinking,omitempty"`
	TimeoutSeconds      *int   `json:"timeoutSeconds,omitempty"`
	AllowUnsafeExternal *bool  `json:"allowUnsafeExternalContent,omitempty"`
}

type PayloadPatch struct {
	Kind                string  `json:"kind"`
	Message             *string `json:"message,omitempty"`
	Model               *string `json:"model,omitempty"`
	Thinking            *string `json:"thinking,omitempty"`
	TimeoutSeconds      *int    `json:"timeoutSeconds,omitempty"`
	AllowUnsafeExternal *bool   `json:"allowUnsafeExternalContent,omitempty"`
}

type JobState struct {
	NextRunAtMs    *int64 `json:"nextRunAtMs,omitempty"`
	RunningAtMs    *int64 `json:"runningAtMs,omitempty"`
	LastRunAtMs    *int64 `json:"lastRunAtMs,omitempty"`
	LastStatus     string `json:"lastStatus,omitempty"`
	LastError      string `json:"lastError,omitempty"`
	LastDurationMs *int64 `json:"lastDurationMs,omitempty"`
}

type Job struct {
	ID             string    `json:"id"`
	AgentID        string    `json:"agentId,omitempty"`
	Name           string    `json:"name"`
	Description    string    `json:"description,omitempty"`
	Enabled        bool      `json:"enabled"`
	DeleteAfterRun bool      `json:"deleteAfterRun,omitempty"`
	CreatedAtMs    int64     `json:"createdAtMs"`
	UpdatedAtMs    int64     `json:"updatedAtMs"`
	Schedule       Schedule  `json:"schedule"`
	Payload        Payload   `json:"payload"`
	Delivery       *Delivery `json:"delivery,omitempty"`
	State          JobState  `json:"state"`
}

type JobCreate struct {
	AgentID        *string   `json:"agentId,omitempty"`
	Name           string    `json:"name,omitempty"`
	Description    *string   `json:"description,omitempty"`
	Enabled        *bool     `json:"enabled,omitempty"`
	DeleteAfterRun *bool     `json:"deleteAfterRun,omitempty"`
	Schedule       Schedule  `json:"schedule"`
	Payload        Payload   `json:"payload"`
	Delivery       *Delivery `json:"delivery,omitempty"`
	State          *JobState `json:"state,omitempty"`
}

type JobPatch struct {
	AgentID        *string        `json:"agentId,omitempty"`
	Name           *string        `json:"name,omitempty"`
	Description    *string        `json:"description,omitempty"`
	Enabled        *bool          `json:"enabled,omitempty"`
	DeleteAfterRun *bool          `json:"deleteAfterRun,omitempty"`
	Schedule       *Schedule      `json:"schedule,omitempty"`
	Payload        *PayloadPatch  `json:"payload,omitempty"`
	Delivery       *DeliveryPatch `json:"delivery,omitempty"`
	State          *JobState      `json:"state,omitempty"`
}

type TimestampValidationResult struct {
	Ok      bool
	Message string
}
