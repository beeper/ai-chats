package sdk

import (
	"context"
	"strings"
	"time"
)

// DefaultApprovalExpiry is the fallback expiry duration when no TTL is specified.
const DefaultApprovalExpiry = 10 * time.Minute

// ApprovalWaitReason maps a completed wait context to the canonical approval reason.
func ApprovalWaitReason(ctx context.Context) string {
	if ctx != nil && ctx.Err() != nil {
		return ApprovalReasonCancelled
	}
	return ApprovalReasonTimeout
}

// ResolveApprovalRequest applies shared approval-request defaults while letting
// the caller control ID generation and policy defaults.
func ResolveApprovalRequest(
	req ApprovalRequest,
	newID func() string,
	defaultTTL time.Duration,
	defaultAllowAlways bool,
) (string, time.Duration, ApprovalPromptPresentation) {
	approvalID := strings.TrimSpace(req.ApprovalID)
	if approvalID == "" && newID != nil {
		approvalID = strings.TrimSpace(newID())
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = defaultTTL
	}
	if ttl <= 0 {
		ttl = DefaultApprovalExpiry
	}
	presentation := ApprovalPromptPresentation{
		Title:       strings.TrimSpace(req.ToolName),
		AllowAlways: defaultAllowAlways,
	}
	if req.Presentation != nil {
		presentation = *req.Presentation
	}
	return approvalID, ttl, presentation
}
