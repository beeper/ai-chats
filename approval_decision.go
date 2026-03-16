package agentremote

import (
	"errors"
	"strings"
)

// Approval decision reason constants.
const (
	ApprovalReasonAllowOnce     = "allow_once"
	ApprovalReasonAllowAlways   = "allow_always"
	ApprovalReasonAutoApproved  = "auto_approved"
	ApprovalReasonDeny          = "deny"
	ApprovalReasonTimeout       = "timeout"
	ApprovalReasonExpired       = "expired"
	ApprovalReasonCancelled     = "cancelled"
	ApprovalReasonDeliveryError = "delivery_error"
)

// ApprovalDecisionPayload is the standardized decision type for all approval flows.
type ApprovalDecisionPayload struct {
	ApprovalID string
	Approved   bool
	Always     bool
	Reason     string
}

// Shared sentinel errors for approval resolution.
var (
	ErrApprovalMissingID      = errors.New("missing approval id")
	ErrApprovalMissingRoom    = errors.New("missing room id")
	ErrApprovalOnlyOwner      = errors.New("only the owner can approve")
	ErrApprovalUnknown        = errors.New("unknown or expired approval id")
	ErrApprovalWrongRoom      = errors.New("approval id does not belong to this room")
	ErrApprovalExpired        = errors.New("approval expired")
	ErrApprovalAlreadyHandled = errors.New("approval already resolved")
)

// ApprovalErrorToastText maps an approval error to a user-facing toast string.
func ApprovalErrorToastText(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrApprovalOnlyOwner):
		return "Only the owner can approve."
	case errors.Is(err, ErrApprovalWrongRoom):
		return "That approval request belongs to a different room."
	case errors.Is(err, ErrApprovalExpired), errors.Is(err, ErrApprovalUnknown):
		return "That approval request is expired or no longer valid."
	case errors.Is(err, ErrApprovalAlreadyHandled):
		return "That approval request was already handled."
	case errors.Is(err, ErrApprovalMissingID):
		return "Missing approval ID."
	case errors.Is(err, ErrApprovalMissingRoom):
		return "Missing room ID."
	default:
		return strings.TrimSpace(err.Error())
	}
}
