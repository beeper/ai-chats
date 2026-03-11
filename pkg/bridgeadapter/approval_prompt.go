package bridgeadapter

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

const ApprovalDecisionKey = "com.beeper.ai.approval_decision"

const (
	ApprovalPromptStateRequested = "approval-requested"
	ApprovalPromptStateResponded = "approval-responded"

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
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	count := 0
	for _, key := range keys {
		if count >= max {
			break
		}
		if value := ValueSummary(values[key]); value != "" {
			details = append(details, ApprovalDetail{
				Label: fmt.Sprintf("%s %s", labelPrefix, key),
				Value: value,
			})
			count++
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

func (o ApprovalOption) prefillKeys() []string {
	keys := o.allKeys()
	if len(keys) == 0 {
		return nil
	}
	return keys
}

func ApprovalPromptOptions(allowAlways bool) []ApprovalOption {
	options := []ApprovalOption{
		{
			ID:       "allow_once",
			Key:      "✅",
			Label:    "Approve once",
			Approved: true,
			Reason:   "allow_once",
		},
		{
			ID:       "deny",
			Key:      "❌",
			Label:    "Deny",
			Approved: false,
			Reason:   "deny",
		},
	}
	if !allowAlways {
		return options
	}
	return []ApprovalOption{
		options[0],
		{
			ID:       "allow_always",
			Key:      "🔁",
			Label:    "Always allow",
			Approved: true,
			Always:   true,
			Reason:   "allow_always",
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
		hints = append(hints, fmt.Sprintf("%s %s", key, label))
	}
	return hints
}

func approvalPromptTitle(presentation ApprovalPromptPresentation, fallbackToolName string) string {
	title := strings.TrimSpace(presentation.Title)
	if title != "" {
		return title
	}
	fallbackToolName = strings.TrimSpace(fallbackToolName)
	if fallbackToolName == "" {
		return "tool"
	}
	return fallbackToolName
}

func BuildApprovalPromptBody(presentation ApprovalPromptPresentation, options []ApprovalOption) string {
	title := approvalPromptTitle(presentation, "")
	lines := []string{fmt.Sprintf("Approval required: %s", title)}
	for _, detail := range presentation.Details {
		label := strings.TrimSpace(detail.Label)
		value := strings.TrimSpace(detail.Value)
		if label == "" || value == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: %s", label, value))
	}
	hints := renderApprovalOptionHints(options)
	if len(hints) == 0 {
		lines = append(lines, "React to approve or deny.")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "React with: "+strings.Join(hints, ", "))
	return strings.Join(lines, "\n")
}

func BuildApprovalResponseBody(presentation ApprovalPromptPresentation, decision ApprovalDecisionPayload) string {
	title := approvalPromptTitle(presentation, "")
	lines := []string{fmt.Sprintf("Approval required: %s", title)}
	for _, detail := range presentation.Details {
		label := strings.TrimSpace(detail.Label)
		value := strings.TrimSpace(detail.Value)
		if label == "" || value == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: %s", label, value))
	}
	outcome := "denied"
	if decision.Approved {
		outcome = "approved"
	}
	if decision.Always && decision.Approved {
		outcome = "approved (always allow)"
	}
	reason := strings.TrimSpace(decision.Reason)
	if reason == "" {
		lines = append(lines, "Decision: "+outcome)
	} else {
		lines = append(lines, fmt.Sprintf("Decision: %s (reason: %s)", outcome, reason))
	}
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

func BuildApprovalPromptMessage(params ApprovalPromptMessageParams) ApprovalPromptMessage {
	approvalID := strings.TrimSpace(params.ApprovalID)
	toolCallID := strings.TrimSpace(params.ToolCallID)
	toolName := strings.TrimSpace(params.ToolName)
	turnID := strings.TrimSpace(params.TurnID)
	if toolCallID == "" {
		toolCallID = approvalID
	}
	if toolName == "" {
		toolName = "tool"
	}
	presentation := normalizeApprovalPromptPresentation(params.Presentation, toolName)
	var options []ApprovalOption
	if len(params.Options) > 0 {
		options = normalizeApprovalOptions(params.Options)
	} else {
		options = normalizeApprovalOptions(ApprovalPromptOptions(presentation.AllowAlways))
	}
	body := BuildApprovalPromptBody(presentation, options)
	metadata := map[string]any{
		"approvalId": approvalID,
	}
	if turnID != "" {
		metadata["turn_id"] = turnID
	}
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
	approvalMeta := map[string]any{
		"kind":            "request",
		"approvalId":      approvalID,
		"toolCallId":      toolCallID,
		"toolName":        toolName,
		"options":         optionsToRaw(options),
		"renderedOptions": renderApprovalOptionHints(options),
		"presentation":    presentationToRaw(presentation),
	}
	if turnID != "" {
		approvalMeta["turnId"] = turnID
	}
	if !params.ExpiresAt.IsZero() {
		approvalMeta["expiresAt"] = params.ExpiresAt.UnixMilli()
	}
	raw := map[string]any{
		"msgtype":                event.MsgNotice,
		"body":                   body,
		"m.mentions":             map[string]any{},
		matrixevents.BeeperAIKey: uiMessage,
		ApprovalDecisionKey:      approvalMeta,
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
	approvalID := strings.TrimSpace(params.ApprovalID)
	toolCallID := strings.TrimSpace(params.ToolCallID)
	toolName := strings.TrimSpace(params.ToolName)
	turnID := strings.TrimSpace(params.TurnID)
	if toolCallID == "" {
		toolCallID = approvalID
	}
	if toolName == "" {
		toolName = "tool"
	}
	presentation := normalizeApprovalPromptPresentation(params.Presentation, toolName)
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
	metadata := map[string]any{
		"approvalId": approvalID,
	}
	if turnID != "" {
		metadata["turn_id"] = turnID
	}
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
	approvalMeta := map[string]any{
		"kind":         "response",
		"approvalId":   approvalID,
		"toolCallId":   toolCallID,
		"toolName":     toolName,
		"presentation": presentationToRaw(presentation),
		"approved":     decision.Approved,
		"always":       decision.Always,
	}
	if strings.TrimSpace(decision.Reason) != "" {
		approvalMeta["reason"] = strings.TrimSpace(decision.Reason)
	}
	if turnID != "" {
		approvalMeta["turnId"] = turnID
	}
	if !params.ExpiresAt.IsZero() {
		approvalMeta["expiresAt"] = params.ExpiresAt.UnixMilli()
	}
	raw := map[string]any{
		"msgtype":                event.MsgNotice,
		"body":                   body,
		"m.mentions":             map[string]any{},
		matrixevents.BeeperAIKey: uiMessage,
		ApprovalDecisionKey:      approvalMeta,
	}
	return ApprovalPromptMessage{
		Body:         body,
		UIMessage:    uiMessage,
		Raw:          raw,
		Presentation: presentation,
	}
}

type ApprovalPromptRegistration struct {
	ApprovalID      string
	RoomID          id.RoomID
	OwnerMXID       id.UserID
	ToolCallID      string
	ToolName        string
	TurnID          string
	Presentation    ApprovalPromptPresentation
	ExpiresAt       time.Time
	Options         []ApprovalOption
	PromptEventID   id.EventID
	PromptMessageID networkid.MessageID
	PromptSenderID  networkid.UserID
}

type ApprovalPromptReactionMatch struct {
	KnownPrompt   bool
	ShouldResolve bool
	ApprovalID    string
	Decision      ApprovalDecisionPayload
	RejectReason  string
	Prompt        ApprovalPromptRegistration
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
	if !presentation.AllowAlways && strings.TrimSpace(presentation.Title) == "" && len(presentation.Details) == 0 {
		presentation.AllowAlways = true
	}
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

func normalizeApprovalOptions(options []ApprovalOption) []ApprovalOption {
	if len(options) == 0 {
		options = DefaultApprovalOptions()
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
		return DefaultApprovalOptions()
	}
	return out
}

func normalizeReactionKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	return variationselector.Remove(key)
}
