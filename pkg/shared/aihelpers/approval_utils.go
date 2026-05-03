package aihelpers

import (
	"context"
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
