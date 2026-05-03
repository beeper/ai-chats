package sdk

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	ApprovalPromptStateRequested = "approval-requested"
	ApprovalPromptStateResponded = "approval-responded"

	ApprovalReactionKeyAllowOnce   = "approval.allow_once"
	ApprovalReactionKeyAllowAlways = "approval.allow_always"
	ApprovalReactionKeyDeny        = "approval.deny"

	ApprovalReactionAliasAllowOnce   = "👍"
	ApprovalReactionAliasAllowAlways = "♾️"
	ApprovalReactionAliasDeny        = "👎"

	RejectReasonOwnerOnly     = "only_owner"
	RejectReasonExpired       = "expired"
	RejectReasonInvalidOption = "invalid_option"
)

type ApprovalOption struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	FallbackKey string `json:"fallback_key,omitempty"`
	Label       string `json:"label,omitempty"`
	Approved    bool   `json:"approved"`
	Always      bool   `json:"always,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type ApprovalDetail struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type ApprovalPromptPresentation struct {
	Title       string           `json:"title"`
	Details     []ApprovalDetail `json:"details,omitempty"`
	AllowAlways bool             `json:"allowAlways,omitempty"`
}

// AppendDetailsFromMap appends approval details from a string-keyed map, sorted by key,
// with a truncation notice if the map exceeds max entries.
func AppendDetailsFromMap(details []ApprovalDetail, labelPrefix string, values map[string]any, max int) []ApprovalDetail {
	if len(values) == 0 || max <= 0 {
		return details
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	added := 0
	for _, key := range keys {
		if added >= max {
			break
		}
		if value := ValueSummary(values[key]); value != "" {
			details = append(details, ApprovalDetail{
				Label: fmt.Sprintf("%s %s", labelPrefix, strings.TrimSpace(key)),
				Value: value,
			})
			added++
		}
	}
	if len(keys) > max {
		details = append(details, ApprovalDetail{
			Label: "Input",
			Value: fmt.Sprintf("%d additional field(s)", len(keys)-max),
		})
	}
	return details
}

// ValueSummary returns a human-readable summary of a value for approval detail display.
func ValueSummary(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case *string:
		if typed == nil {
			return ""
		}
		return strings.TrimSpace(*typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case int, int8, int16, int32, int64, float32, float64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%v", typed)
	case []string:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				items = append(items, trimmed)
			}
		}
		if len(items) == 0 {
			return ""
		}
		if len(items) > 3 {
			return fmt.Sprintf("%s (+%d more)", strings.Join(items[:3], ", "), len(items)-3)
		}
		return strings.Join(items, ", ")
	case []any:
		if len(typed) == 0 {
			return ""
		}
		return fmt.Sprintf("%d item(s)", len(typed))
	case map[string]any:
		if len(typed) == 0 {
			return ""
		}
		return fmt.Sprintf("%d field(s)", len(typed))
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		serialized := strings.TrimSpace(string(encoded))
		if len(serialized) > 160 {
			return serialized[:160] + "..."
		}
		return serialized
	}
}

func (o ApprovalOption) decisionReason() string {
	if reason := strings.TrimSpace(o.Reason); reason != "" {
		return reason
	}
	return strings.TrimSpace(o.ID)
}

func (o ApprovalOption) allKeys() []string {
	primary := normalizeReactionKey(o.Key)
	fallback := normalizeReactionKey(o.FallbackKey)
	switch {
	case primary == "" && fallback == "":
		return nil
	case primary == "":
		return []string{fallback}
	case fallback == "", fallback == primary:
		return []string{primary}
	default:
		return []string{primary, fallback}
	}
}

func ApprovalPromptOptions(allowAlways bool) []ApprovalOption {
	options := []ApprovalOption{
		{
			ID:          ApprovalReasonAllowOnce,
			Key:         ApprovalReactionKeyAllowOnce,
			FallbackKey: ApprovalReactionAliasAllowOnce,
			Label:       "Approve once",
			Approved:    true,
			Reason:      ApprovalReasonAllowOnce,
		},
		{
			ID:          ApprovalReasonDeny,
			Key:         ApprovalReactionKeyDeny,
			FallbackKey: ApprovalReactionAliasDeny,
			Label:       "Deny",
			Approved:    false,
			Reason:      ApprovalReasonDeny,
		},
	}
	if !allowAlways {
		return options
	}
	return []ApprovalOption{
		options[0],
		{
			ID:          ApprovalReasonAllowAlways,
			Key:         ApprovalReactionKeyAllowAlways,
			FallbackKey: ApprovalReactionAliasAllowAlways,
			Label:       "Always allow",
			Approved:    true,
			Always:      true,
			Reason:      ApprovalReasonAllowAlways,
		},
		options[1],
	}
}

func renderApprovalOptionHints(options []ApprovalOption) []string {
	hints := make([]string, 0, len(options))
	for _, opt := range options {
		key := strings.TrimSpace(opt.Key)
		if key == "" {
			key = strings.TrimSpace(opt.FallbackKey)
		}
		label := strings.TrimSpace(opt.Label)
		if key == "" || label == "" {
			continue
		}
		hints = append(hints, fmt.Sprintf("%s = %s", key, label))
	}
	return hints
}

func buildApprovalBodyHeader(presentation ApprovalPromptPresentation) []string {
	title := strings.TrimSpace(presentation.Title)
	if title == "" {
		title = "tool"
	}
	lines := []string{fmt.Sprintf("Approval required: %s", title)}
	for _, detail := range presentation.Details {
		label := strings.TrimSpace(detail.Label)
		value := strings.TrimSpace(detail.Value)
		if label == "" || value == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: %s", label, value))
	}
	return lines
}
