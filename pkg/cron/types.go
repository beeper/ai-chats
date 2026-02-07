package cron

// CronSchedule defines when a cron job should run.
type CronSchedule struct {
	Kind     string `json:"kind"`
	At       string `json:"at,omitempty"`
	EveryMs  int64  `json:"everyMs,omitempty"`
	AnchorMs *int64 `json:"anchorMs,omitempty"`
	Expr     string `json:"expr,omitempty"`
	TZ       string `json:"tz,omitempty"`
}

// CronSessionTarget defines where a cron job runs.
type CronSessionTarget string

const (
	CronSessionMain     CronSessionTarget = "main"
	CronSessionIsolated CronSessionTarget = "isolated"
)

// CronWakeMode defines how the heartbeat is triggered after a main job.
type CronWakeMode string

const (
	CronWakeNextHeartbeat CronWakeMode = "next-heartbeat"
	CronWakeNow           CronWakeMode = "now"
)

// CronDeliveryMode defines isolated job delivery behavior.
type CronDeliveryMode string

const (
	CronDeliveryNone     CronDeliveryMode = "none"
	CronDeliveryAnnounce CronDeliveryMode = "announce"
)

// CronDelivery controls how isolated runs announce results.
type CronDelivery struct {
	Mode       CronDeliveryMode `json:"mode"`
	Channel    string           `json:"channel,omitempty"`
	To         string           `json:"to,omitempty"`
	BestEffort *bool            `json:"bestEffort,omitempty"`
}

// CronDeliveryPatch defines partial delivery updates.
type CronDeliveryPatch struct {
	Mode       *CronDeliveryMode `json:"mode,omitempty"`
	Channel    *string           `json:"channel,omitempty"`
	To         *string           `json:"to,omitempty"`
	BestEffort *bool             `json:"bestEffort,omitempty"`
}

// CronPayload defines the job action.
type CronPayload struct {
	Kind                string `json:"kind"`
	Text                string `json:"text,omitempty"`
	Message             string `json:"message,omitempty"`
	Model               string `json:"model,omitempty"`
	Thinking            string `json:"thinking,omitempty"`
	TimeoutSeconds      *int   `json:"timeoutSeconds,omitempty"`
	AllowUnsafeExternal *bool  `json:"allowUnsafeExternalContent,omitempty"`
}

// CronPayloadPatch defines partial payload updates.
type CronPayloadPatch struct {
	Kind                string  `json:"kind"`
	Text                *string `json:"text,omitempty"`
	Message             *string `json:"message,omitempty"`
	Model               *string `json:"model,omitempty"`
	Thinking            *string `json:"thinking,omitempty"`
	TimeoutSeconds      *int    `json:"timeoutSeconds,omitempty"`
	AllowUnsafeExternal *bool   `json:"allowUnsafeExternalContent,omitempty"`
}

// CronJobState tracks runtime state.
type CronJobState struct {
	NextRunAtMs    *int64 `json:"nextRunAtMs,omitempty"`
	RunningAtMs    *int64 `json:"runningAtMs,omitempty"`
	LastRunAtMs    *int64 `json:"lastRunAtMs,omitempty"`
	LastStatus     string `json:"lastStatus,omitempty"`
	LastError      string `json:"lastError,omitempty"`
	LastDurationMs *int64 `json:"lastDurationMs,omitempty"`
}

// CronJob defines a stored job.
type CronJob struct {
	ID             string            `json:"id"`
	AgentID        string            `json:"agentId,omitempty"`
	Name           string            `json:"name"`
	Description    string            `json:"description,omitempty"`
	Enabled        bool              `json:"enabled"`
	DeleteAfterRun bool              `json:"deleteAfterRun,omitempty"`
	CreatedAtMs    int64             `json:"createdAtMs"`
	UpdatedAtMs    int64             `json:"updatedAtMs"`
	Schedule       CronSchedule      `json:"schedule"`
	SessionTarget  CronSessionTarget `json:"sessionTarget"`
	WakeMode       CronWakeMode      `json:"wakeMode"`
	Payload        CronPayload       `json:"payload"`
	Delivery       *CronDelivery     `json:"delivery,omitempty"`
	State          CronJobState      `json:"state"`
}

// CronStoreFile defines the JSON store format.
type CronStoreFile struct {
	Version int       `json:"version"`
	Jobs    []CronJob `json:"jobs"`
}

// CronJobCreate is input for creating jobs.
type CronJobCreate struct {
	AgentID        *string           `json:"agentId,omitempty"`
	Name           string            `json:"name,omitempty"`
	Description    *string           `json:"description,omitempty"`
	Enabled        *bool             `json:"enabled,omitempty"`
	DeleteAfterRun *bool             `json:"deleteAfterRun,omitempty"`
	Schedule       CronSchedule      `json:"schedule"`
	SessionTarget  CronSessionTarget `json:"sessionTarget"`
	WakeMode       CronWakeMode      `json:"wakeMode,omitempty"`
	Payload        CronPayload       `json:"payload"`
	Delivery       *CronDelivery     `json:"delivery,omitempty"`
	State          *CronJobState     `json:"state,omitempty"`
}

// CronJobPatch defines partial updates.
type CronJobPatch struct {
	AgentID        *string            `json:"agentId,omitempty"`
	Name           *string            `json:"name,omitempty"`
	Description    *string            `json:"description,omitempty"`
	Enabled        *bool              `json:"enabled,omitempty"`
	DeleteAfterRun *bool              `json:"deleteAfterRun,omitempty"`
	Schedule       *CronSchedule      `json:"schedule,omitempty"`
	SessionTarget  *CronSessionTarget `json:"sessionTarget,omitempty"`
	WakeMode       *CronWakeMode      `json:"wakeMode,omitempty"`
	Payload        *CronPayloadPatch  `json:"payload,omitempty"`
	Delivery       *CronDeliveryPatch `json:"delivery,omitempty"`
	State          *CronJobState      `json:"state,omitempty"`
}
