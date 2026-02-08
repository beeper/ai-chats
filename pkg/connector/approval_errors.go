package connector

import "errors"

// Sentinel errors for approval resolution, so callers can map them to UI/toasts
// without parsing error strings.
var (
	ErrApprovalMissingID      = errors.New("missing approval id")
	ErrApprovalMissingRoom    = errors.New("missing room id")
	ErrApprovalOnlyOwner      = errors.New("only the owner can approve")
	ErrApprovalUnknown        = errors.New("unknown or expired approval id")
	ErrApprovalWrongRoom      = errors.New("approval id does not belong to this room")
	ErrApprovalExpired        = errors.New("approval expired")
	ErrApprovalAlreadyHandled = errors.New("approval already resolved")
)
