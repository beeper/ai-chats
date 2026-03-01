package runtime

// InboundContext is the canonical normalized inbound payload used by runtime modules.
type InboundContext struct {
	Provider          string
	Surface           string
	ChatType          string
	ChatID            string
	ConversationLabel string
	SenderLabel       string
	SenderID          string
	MessageID         string
	MessageIDFull     string
	ReplyToID         string
	ThreadID          string
	Body              string
	BodyForAgent      string
	BodyForCommands   string
	RawBody           string
	ThreadStarterBody string
	CommandAuthorized bool
	MediaURLs         []string
	MediaTypes        []string
	TimestampMs       int64
}

// ReplyDirectiveResult describes parsed reply/silent/audio directives.
type ReplyDirectiveResult struct {
	Text              string
	ReplyToID         string
	ReplyToExplicitID string
	ReplyToCurrent    bool
	HasReplyTag       bool
	AudioAsVoice      bool
	IsSilent          bool
}

// StreamingDirectiveResult is a streaming-safe directive parse result.
type StreamingDirectiveResult struct {
	Text              string
	ReplyToExplicitID string
	ReplyToCurrent    bool
	HasReplyTag       bool
	AudioAsVoice      bool
	IsSilent          bool
}

// ReplyPayload is a normalized assistant payload fragment.
type ReplyPayload struct {
	Text           string
	MediaURL       string
	MediaURLs      []string
	ReplyToID      string
	ReplyToTag     bool
	ReplyToCurrent bool
	AudioAsVoice   bool
	IsError        bool
}

// ReplyToMode controls how reply IDs are applied to payloads.
type ReplyToMode string

const (
	ReplyToModeOff   ReplyToMode = "off"
	ReplyToModeFirst ReplyToMode = "first"
	ReplyToModeAll   ReplyToMode = "all"
)

// QueueMode models OpenClaw-like queue behavior presets.
type QueueMode string

const (
	QueueModeInterrupt    QueueMode = "interrupt"
	QueueModeBacklog      QueueMode = "backlog"
	QueueModeSteerBacklog QueueMode = "steer-backlog"
)

// QueueDecisionAction is the runtime's final queue decision.
type QueueDecisionAction string

const (
	QueueActionRunNow          QueueDecisionAction = "run_now"
	QueueActionEnqueue         QueueDecisionAction = "enqueue"
	QueueActionDrop            QueueDecisionAction = "drop"
	QueueActionInterruptAndRun QueueDecisionAction = "interrupt_and_run"
)

// QueueDecision is a deterministic decision output for queue handling.
type QueueDecision struct {
	Action QueueDecisionAction
	Reason string
}

// FailureClass groups error types for fallback and UX handling.
type FailureClass string

const (
	FailureClassUnknown         FailureClass = "unknown"
	FailureClassAuth            FailureClass = "auth"
	FailureClassRateLimit       FailureClass = "rate_limit"
	FailureClassTimeout         FailureClass = "timeout"
	FailureClassNetwork         FailureClass = "network"
	FailureClassContextOverflow FailureClass = "context_overflow"
	FailureClassProviderHard    FailureClass = "provider_hard"
)

// ToolApprovalState tracks the approval lifecycle for tools.
type ToolApprovalState string

const (
	ToolApprovalRequired ToolApprovalState = "required"
	ToolApprovalPending  ToolApprovalState = "pending"
	ToolApprovalApproved ToolApprovalState = "approved"
	ToolApprovalDenied   ToolApprovalState = "denied"
	ToolApprovalTimedOut ToolApprovalState = "timed_out"
	ToolApprovalStale    ToolApprovalState = "stale"
)

// ToolApprovalDecision is a policy decision output.
type ToolApprovalDecision struct {
	State   ToolApprovalState
	Reason  string
	Tool    string
	CallID  string
	IsError bool
}
