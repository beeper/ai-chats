package aihelpers

import (
	"strings"
)

func BuildApprovalPromptBody(presentation ApprovalPromptPresentation, options []ApprovalOption) string {
	lines := buildApprovalBodyHeader(presentation)
	hints := renderApprovalOptionHints(options)
	if len(hints) == 0 {
		lines = append(lines, "React to approve or deny.")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "React with: "+strings.Join(hints, ", "))
	return strings.Join(lines, "\n")
}

func BuildApprovalResponseBody(presentation ApprovalPromptPresentation, decision ApprovalDecisionPayload) string {
	lines := buildApprovalBodyHeader(presentation)
	outcome := ""
	reason := ""
	if decision.Approved {
		if decision.Always {
			outcome = "approved (always allow)"
		} else {
			outcome = "approved"
		}
	} else {
		reason = strings.TrimSpace(decision.Reason)
		switch reason {
		case ApprovalReasonTimeout:
			outcome, reason = "timed out", ""
		case ApprovalReasonExpired:
			outcome, reason = "expired", ""
		case ApprovalReasonDeliveryError:
			outcome, reason = "delivery error", ""
		case ApprovalReasonCancelled:
			outcome, reason = "cancelled", ""
		case "":
			outcome = "denied"
		default:
			outcome = "denied"
		}
	}
	line := "Decision: " + outcome
	if reason != "" {
		line += " (reason: " + reason + ")"
	}
	lines = append(lines, line)
	return strings.Join(lines, "\n")
}
