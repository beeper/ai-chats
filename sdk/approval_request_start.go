package sdk

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"
)

type ApprovalPromptContext struct {
	TurnID            string
	ReplyToEventID    id.EventID
	ThreadRootEventID id.EventID
}

// ApprovalRequestEmitter is the minimal stream UI surface needed when an
// approval request becomes pending.
type ApprovalRequestEmitter interface {
	EmitRequest(ctx context.Context, approvalID, toolCallID string)
}

type StartApprovalRequestParams[D any] struct {
	Portal             *bridgev2.Portal
	OwnerMXID          id.UserID
	SendPrompt         bool
	Request            ApprovalRequest
	NewID              func() string
	DefaultTTL         time.Duration
	DefaultAllowAlways bool
	PromptContext      ApprovalPromptContext
	Emitter            ApprovalRequestEmitter
	EmitRequest        func(context.Context, string, string)
	Data               D
}

type StartedApprovalRequest[D any] struct {
	ApprovalID   string
	TTL          time.Duration
	Presentation ApprovalPromptPresentation
	Pending      *Pending[D]
	Created      bool
	PromptSent   bool
}

func (f *ApprovalFlow[D]) StartApprovalRequest(ctx context.Context, params StartApprovalRequestParams[D]) StartedApprovalRequest[D] {
	if f == nil {
		return StartedApprovalRequest[D]{}
	}
	approvalID := strings.TrimSpace(params.Request.ApprovalID)
	if approvalID == "" && params.NewID != nil {
		approvalID = strings.TrimSpace(params.NewID())
	}
	ttl := params.Request.TTL
	if ttl <= 0 {
		ttl = params.DefaultTTL
	}
	if ttl <= 0 {
		ttl = DefaultApprovalExpiry
	}
	presentation := ApprovalPromptPresentation{
		Title:       strings.TrimSpace(params.Request.ToolName),
		AllowAlways: params.DefaultAllowAlways,
	}
	if params.Request.Presentation != nil {
		presentation = *params.Request.Presentation
	}
	started := StartedApprovalRequest[D]{
		ApprovalID:   approvalID,
		TTL:          ttl,
		Presentation: presentation,
	}
	pending, created := f.Register(approvalID, ttl, params.Data)
	started.Pending = pending
	started.Created = created
	if !created {
		return started
	}
	if params.Emitter != nil {
		params.Emitter.EmitRequest(ctx, approvalID, params.Request.ToolCallID)
	} else if params.EmitRequest != nil {
		params.EmitRequest(ctx, approvalID, params.Request.ToolCallID)
	}
	if !params.SendPrompt || params.Portal == nil || params.Portal.MXID == "" || params.OwnerMXID == "" {
		return started
	}
	if err := f.SendPrompt(ctx, params.Portal, SendPromptParams{
		ApprovalPromptMessageParams: ApprovalPromptMessageParams{
			ApprovalID:        approvalID,
			ToolCallID:        params.Request.ToolCallID,
			ToolName:          params.Request.ToolName,
			TurnID:            params.PromptContext.TurnID,
			Presentation:      presentation,
			ReplyToEventID:    params.PromptContext.ReplyToEventID,
			ThreadRootEventID: params.PromptContext.ThreadRootEventID,
			ExpiresAt:         time.Now().Add(ttl),
		},
		RoomID:    params.Portal.MXID,
		OwnerMXID: params.OwnerMXID,
	}); err != nil {
		return started
	}
	started.PromptSent = true
	return started
}
