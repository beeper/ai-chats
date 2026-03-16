package agentremote

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.mau.fi/util/variationselector"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/matrixevents"
)

const (
	ApprovalPromptStateRequested = "approval-requested"
	ApprovalPromptStateResponded = "approval-responded"

	ApprovalReactionKeyAllowOnce   = "approval.allow_once"
	ApprovalReactionKeyAllowAlways = "approval.allow_always"
	ApprovalReactionKeyDeny        = "approval.deny"

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
			ID:       ApprovalReasonAllowOnce,
			Key:      ApprovalReactionKeyAllowOnce,
			Label:    "Approve once",
			Approved: true,
			Reason:   ApprovalReasonAllowOnce,
		},
		{
			ID:       ApprovalReasonDeny,
			Key:      ApprovalReactionKeyDeny,
			Label:    "Deny",
			Approved: false,
			Reason:   ApprovalReasonDeny,
		},
	}
	if !allowAlways {
		return options
	}
	return []ApprovalOption{
		options[0],
		{
			ID:       ApprovalReasonAllowAlways,
			Key:      ApprovalReactionKeyAllowAlways,
			Label:    "Always allow",
			Approved: true,
			Always:   true,
			Reason:   ApprovalReasonAllowAlways,
		},
		options[1],
	}
}

func DefaultApprovalOptions() []ApprovalOption {
	return ApprovalPromptOptions(true)
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
	outcome, reason := approvalDecisionOutcome(decision)
	line := "Decision: " + outcome
	if reason != "" {
		line += " (reason: " + reason + ")"
	}
	lines = append(lines, line)
	return strings.Join(lines, "\n")
}

type ApprovalPromptMessageParams struct {
	ApprovalID     string
	ToolCallID     string
	ToolName       string
	TurnID         string
	Presentation   ApprovalPromptPresentation
	ReplyToEventID id.EventID
	ExpiresAt      time.Time
	Options        []ApprovalOption
}

type ApprovalResponsePromptMessageParams struct {
	ApprovalID   string
	ToolCallID   string
	ToolName     string
	TurnID       string
	Presentation ApprovalPromptPresentation
	Options      []ApprovalOption
	Decision     ApprovalDecisionPayload
	ExpiresAt    time.Time
}

type ApprovalPromptMessage struct {
	Body         string
	UIMessage    map[string]any
	Raw          map[string]any
	Presentation ApprovalPromptPresentation
	Options      []ApprovalOption
}

type normalizedPromptFields struct {
	approvalID   string
	toolCallID   string
	toolName     string
	turnID       string
	presentation ApprovalPromptPresentation
	options      []ApprovalOption
}

func normalizePromptFields(approvalID, toolCallID, toolName, turnID string, presentation ApprovalPromptPresentation, options []ApprovalOption) normalizedPromptFields {
	approvalID = strings.TrimSpace(approvalID)
	toolCallID = strings.TrimSpace(toolCallID)
	toolName = strings.TrimSpace(toolName)
	turnID = strings.TrimSpace(turnID)
	if toolCallID == "" {
		toolCallID = approvalID
	}
	if toolName == "" {
		toolName = "tool"
	}
	p := normalizeApprovalPromptPresentation(presentation, toolName)
	return normalizedPromptFields{
		approvalID:   approvalID,
		toolCallID:   toolCallID,
		toolName:     toolName,
		turnID:       turnID,
		presentation: p,
		options:      normalizeApprovalOptions(options, ApprovalPromptOptions(p.AllowAlways)),
	}
}

func BuildApprovalPromptMessage(params ApprovalPromptMessageParams) ApprovalPromptMessage {
	f := normalizePromptFields(params.ApprovalID, params.ToolCallID, params.ToolName, params.TurnID, params.Presentation, params.Options)
	approvalID, toolCallID, toolName, turnID := f.approvalID, f.toolCallID, f.toolName, f.turnID
	presentation, options := f.presentation, f.options
	body := BuildApprovalPromptBody(presentation, options)
	metadata := approvalMessageMetadata(approvalID, turnID, presentation, options, nil, params.ExpiresAt)
	uiMessage := map[string]any{
		"id":       approvalID,
		"role":     "assistant",
		"metadata": metadata,
		"parts": []map[string]any{{
			"type":       "dynamic-tool",
			"toolName":   toolName,
			"toolCallId": toolCallID,
			"state":      ApprovalPromptStateRequested,
			"approval": map[string]any{
				"id": approvalID,
			},
		}},
	}
	raw := map[string]any{
		"msgtype":                event.MsgNotice,
		"body":                   body,
		"m.mentions":             map[string]any{},
		matrixevents.BeeperAIKey: uiMessage,
	}
	if params.ReplyToEventID != "" {
		raw["m.relates_to"] = map[string]any{
			"m.in_reply_to": map[string]any{
				"event_id": params.ReplyToEventID.String(),
			},
		}
	}
	return ApprovalPromptMessage{
		Body:         body,
		UIMessage:    uiMessage,
		Raw:          raw,
		Presentation: presentation,
		Options:      options,
	}
}

func BuildApprovalResponsePromptMessage(params ApprovalResponsePromptMessageParams) ApprovalPromptMessage {
	f := normalizePromptFields(params.ApprovalID, params.ToolCallID, params.ToolName, params.TurnID, params.Presentation, params.Options)
	approvalID, toolCallID, toolName, turnID := f.approvalID, f.toolCallID, f.toolName, f.turnID
	presentation := f.presentation
	decision := params.Decision
	decision.ApprovalID = strings.TrimSpace(decision.ApprovalID)
	if decision.ApprovalID == "" {
		decision.ApprovalID = approvalID
	}
	body := BuildApprovalResponseBody(presentation, decision)
	approvalPayload := map[string]any{
		"id":       approvalID,
		"approved": decision.Approved,
	}
	if decision.Always {
		approvalPayload["always"] = true
	}
	if strings.TrimSpace(decision.Reason) != "" {
		approvalPayload["reason"] = strings.TrimSpace(decision.Reason)
	}
	options := f.options
	metadata := approvalMessageMetadata(approvalID, turnID, presentation, options, &decision, params.ExpiresAt)
	uiMessage := map[string]any{
		"id":       approvalID,
		"role":     "assistant",
		"metadata": metadata,
		"parts": []map[string]any{{
			"type":       "dynamic-tool",
			"toolName":   toolName,
			"toolCallId": toolCallID,
			"state":      ApprovalPromptStateResponded,
			"approval":   approvalPayload,
		}},
	}
	raw := map[string]any{
		"msgtype":                event.MsgNotice,
		"body":                   body,
		"m.mentions":             map[string]any{},
		matrixevents.BeeperAIKey: uiMessage,
	}
	return ApprovalPromptMessage{
		Body:         body,
		UIMessage:    uiMessage,
		Raw:          raw,
		Presentation: presentation,
		Options:      options,
	}
}

func approvalMessageMetadata(
	approvalID, turnID string,
	presentation ApprovalPromptPresentation,
	options []ApprovalOption,
	decision *ApprovalDecisionPayload,
	expiresAt time.Time,
) map[string]any {
	metadata := map[string]any{
		"approvalId": approvalID,
	}
	if turnID != "" {
		metadata["turn_id"] = turnID
	}
	approval := map[string]any{
		"id":           approvalID,
		"options":      optionsToRaw(options),
		"renderedKeys": renderApprovalOptionHints(options),
		"presentation": presentationToRaw(presentation),
	}
	if !expiresAt.IsZero() {
		approval["expiresAt"] = expiresAt.UnixMilli()
	}
	if decision != nil {
		approval["approved"] = decision.Approved
		if decision.Always {
			approval["always"] = true
		}
		if strings.TrimSpace(decision.Reason) != "" {
			approval["reason"] = strings.TrimSpace(decision.Reason)
		}
	}
	metadata["approval"] = approval
	return metadata
}

func approvalDecisionOutcome(decision ApprovalDecisionPayload) (string, string) {
	if decision.Approved {
		if decision.Always {
			return "approved (always allow)", ""
		}
		return "approved", ""
	}
	reason := strings.TrimSpace(decision.Reason)
	switch reason {
	case ApprovalReasonTimeout:
		return "timed out", ""
	case ApprovalReasonExpired:
		return "expired", ""
	case ApprovalReasonDeliveryError:
		return "delivery error", ""
	case ApprovalReasonCancelled:
		return "cancelled", ""
	case "":
		return "denied", ""
	default:
		return "denied", reason
	}
}

type ApprovalPromptRegistration struct {
	ApprovalID      string
	RoomID          id.RoomID
	OwnerMXID       id.UserID
	ToolCallID      string
	ToolName        string
	TurnID          string
	PromptVersion   uint64
	Presentation    ApprovalPromptPresentation
	ExpiresAt       time.Time
	Options         []ApprovalOption
	PromptEventID   id.EventID
	PromptMessageID networkid.MessageID
	PromptSenderID  networkid.UserID
}

type ApprovalPromptReactionMatch struct {
	KnownPrompt            bool
	ShouldResolve          bool
	ApprovalID             string
	Decision               ApprovalDecisionPayload
	RejectReason           string
	Prompt                 ApprovalPromptRegistration
	MirrorDecisionReaction bool
	RedactResolvedReaction bool
}

func optionsToRaw(options []ApprovalOption) []map[string]any {
	if len(options) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(options))
	for _, option := range options {
		entry := map[string]any{
			"id":       option.ID,
			"key":      option.Key,
			"approved": option.Approved,
		}
		if option.Always {
			entry["always"] = true
		}
		if strings.TrimSpace(option.FallbackKey) != "" {
			entry["fallback_key"] = option.FallbackKey
		}
		if strings.TrimSpace(option.Label) != "" {
			entry["label"] = option.Label
		}
		if strings.TrimSpace(option.Reason) != "" {
			entry["reason"] = option.Reason
		}
		out = append(out, entry)
	}
	return out
}

func presentationToRaw(p ApprovalPromptPresentation) map[string]any {
	out := map[string]any{
		"title": p.Title,
	}
	if p.AllowAlways {
		out["allowAlways"] = true
	}
	if len(p.Details) > 0 {
		details := make([]map[string]any, 0, len(p.Details))
		for _, detail := range p.Details {
			if strings.TrimSpace(detail.Label) == "" || strings.TrimSpace(detail.Value) == "" {
				continue
			}
			details = append(details, map[string]any{
				"label": detail.Label,
				"value": detail.Value,
			})
		}
		if len(details) > 0 {
			out["details"] = details
		}
	}
	return out
}

func normalizeApprovalPromptPresentation(presentation ApprovalPromptPresentation, fallbackToolName string) ApprovalPromptPresentation {
	presentation.Title = strings.TrimSpace(presentation.Title)
	if presentation.Title == "" {
		fallbackToolName = strings.TrimSpace(fallbackToolName)
		if fallbackToolName == "" {
			fallbackToolName = "tool"
		}
		presentation.Title = fallbackToolName
	}
	if len(presentation.Details) == 0 {
		return presentation
	}
	normalized := make([]ApprovalDetail, 0, len(presentation.Details))
	for _, detail := range presentation.Details {
		detail.Label = strings.TrimSpace(detail.Label)
		detail.Value = strings.TrimSpace(detail.Value)
		if detail.Label == "" || detail.Value == "" {
			continue
		}
		normalized = append(normalized, detail)
	}
	presentation.Details = normalized
	return presentation
}

func normalizeApprovalOptions(options []ApprovalOption, fallback []ApprovalOption) []ApprovalOption {
	allowAlways := true
	switch {
	case len(options) > 0:
		allowAlways = approvalOptionsAllowAlways(options)
	case len(fallback) > 0:
		allowAlways = approvalOptionsAllowAlways(fallback)
	}
	if len(options) == 0 {
		options = fallback
	}
	if len(options) == 0 {
		return ApprovalPromptOptions(allowAlways)
	}
	out := make([]ApprovalOption, 0, len(options))
	for _, option := range options {
		option.ID = strings.TrimSpace(option.ID)
		option.Key = normalizeReactionKey(option.Key)
		option.FallbackKey = normalizeReactionKey(option.FallbackKey)
		option.Label = strings.TrimSpace(option.Label)
		option.Reason = strings.TrimSpace(option.Reason)
		if option.ID == "" {
			continue
		}
		if option.Key == "" && option.FallbackKey == "" {
			continue
		}
		if option.Label == "" {
			option.Label = option.ID
		}
		out = append(out, option)
	}
	if len(out) == 0 {
		return ApprovalPromptOptions(allowAlways)
	}
	return out
}

func approvalOptionsAllowAlways(options []ApprovalOption) bool {
	for _, option := range options {
		if strings.TrimSpace(option.ID) == "allow_always" || option.Always {
			return true
		}
	}
	return false
}

// AddOptionalDetail appends an approval detail from an optional string pointer.
// If the pointer is nil or empty, input and details are returned unchanged.
func AddOptionalDetail(input map[string]any, details []ApprovalDetail, key, label string, ptr *string) (map[string]any, []ApprovalDetail) {
	if v := ValueSummary(ptr); v != "" {
		if input == nil {
			input = make(map[string]any)
		}
		input[key] = v
		details = append(details, ApprovalDetail{Label: label, Value: v})
	}
	return input, details
}

// DecisionToString maps an ApprovalDecisionPayload to one of three upstream
// string values (once/always/deny) based on the decision fields.
func DecisionToString(decision ApprovalDecisionPayload, once, always, deny string) string {
	if !decision.Approved {
		return deny
	}
	if decision.Always {
		return always
	}
	return once
}

func normalizeReactionKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	return variationselector.Remove(key)
}

func isApprovalReactionKey(key string) bool {
	key = normalizeReactionKey(key)
	return strings.HasPrefix(key, "approval.")
}
