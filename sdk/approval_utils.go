package sdk

import "time"

// DefaultApprovalExpiry is the fallback expiry duration when no TTL is specified.
const DefaultApprovalExpiry = 10 * time.Minute

// ComputeApprovalExpiry returns the expiry time based on ttlSeconds, falling
// back to DefaultApprovalExpiry when ttlSeconds <= 0.
func ComputeApprovalExpiry(ttlSeconds int) time.Time {
	if ttlSeconds > 0 {
		return time.Now().Add(time.Duration(ttlSeconds) * time.Second)
	}
	return time.Now().Add(DefaultApprovalExpiry)
}
