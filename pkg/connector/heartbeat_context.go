package connector

import (
	"context"

	"maunium.net/go/mautrix/id"
)

type HeartbeatRunConfig struct {
	Reason           string
	AckMaxChars      int
	ShowOk           bool
	ShowAlerts       bool
	UseIndicator     bool
	IncludeReasoning bool
	ExecEvent        bool
	ResponsePrefix   string
	SessionKey       string
	StoreAgentID     string
	StorePath        string
	PrevUpdatedAt    int64
	TargetRoom       id.RoomID
	TargetReason     string
	SuppressSend     bool
	AgentID          string
	Channel          string
	SuppressSave     bool
}

type HeartbeatRunOutcome struct {
	Status  string
	Reason  string
	Text    string
	Preview string
	Sent    bool
	Silent  bool
	Skipped bool
}

type heartbeatRunContext struct {
	Config   *HeartbeatRunConfig
	ResultCh chan HeartbeatRunOutcome
}

type heartbeatContextKey struct{}

func withHeartbeatRun(ctx context.Context, cfg *HeartbeatRunConfig, ch chan HeartbeatRunOutcome) context.Context {
	return context.WithValue(ctx, heartbeatContextKey{}, &heartbeatRunContext{Config: cfg, ResultCh: ch})
}

func heartbeatRunFromContext(ctx context.Context) *heartbeatRunContext {
	return contextValue[*heartbeatRunContext](ctx, heartbeatContextKey{})
}
