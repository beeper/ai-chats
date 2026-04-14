package sdk

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

// BuildApprovalPresentation constructs an ApprovalPromptPresentation with a
// standard title format of "prefix: subject" (or just "prefix" when subject is
// empty). This centralizes the repeated title-construction pattern used across
// bridge-specific approval builders.
func BuildApprovalPresentation(prefix, subject string, details []ApprovalDetail, allowAlways bool) ApprovalPromptPresentation {
	subject = strings.TrimSpace(subject)
	title := prefix
	if subject != "" {
		title = prefix + ": " + subject
	}
	return ApprovalPromptPresentation{
		Title:       title,
		Details:     details,
		AllowAlways: allowAlways,
	}
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

type ApprovalPromptMessageParams struct {
	ApprovalID        string
	ToolCallID        string
	ToolName          string
	TurnID            string
	Presentation      ApprovalPromptPresentation
	ReplyToEventID    id.EventID
	ThreadRootEventID id.EventID
	ExpiresAt         time.Time
	Options           []ApprovalOption
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
	Content       *event.MessageEventContent
	TopLevelExtra map[string]any
	Body          string
	UIMessage     map[string]any
	Presentation  ApprovalPromptPresentation
	Options       []ApprovalOption
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
	p := presentation
	p.Title = strings.TrimSpace(p.Title)
	if p.Title == "" {
		p.Title = toolName
	}
	if len(p.Details) > 0 {
		normalized := make([]ApprovalDetail, 0, len(p.Details))
		for _, detail := range p.Details {
			detail.Label = strings.TrimSpace(detail.Label)
			detail.Value = strings.TrimSpace(detail.Value)
			if detail.Label == "" || detail.Value == "" {
				continue
			}
			normalized = append(normalized, detail)
		}
		p.Details = normalized
	}
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
	approvalID := f.approvalID
	presentation, options := f.presentation, f.options
	body := BuildApprovalPromptBody(presentation, options)
	metadata := approvalMessageMetadata(approvalID, f.turnID, presentation, options, nil, params.ExpiresAt)
	approvalPayload := map[string]any{
		"id": approvalID,
	}
	uiMessage := buildApprovalUIMessage(f, ApprovalPromptStateRequested, approvalPayload, metadata)
	content := &event.MessageEventContent{
		MsgType:  event.MsgNotice,
		Body:     body,
		Mentions: &event.Mentions{},
	}
	if params.ThreadRootEventID != "" {
		rel := &event.RelatesTo{}
		content.RelatesTo = rel.SetThread(params.ThreadRootEventID, params.ReplyToEventID)
	} else if params.ReplyToEventID != "" {
		rel := &event.RelatesTo{}
		content.RelatesTo = rel.SetReplyTo(params.ReplyToEventID)
	}
	return ApprovalPromptMessage{
		Content:       content,
		TopLevelExtra: map[string]any{matrixevents.BeeperAIKey: uiMessage},
		Body:          body,
		UIMessage:     uiMessage,
		Presentation:  presentation,
		Options:       options,
	}
}

func BuildApprovalResponsePromptMessage(params ApprovalResponsePromptMessageParams) ApprovalPromptMessage {
	f := normalizePromptFields(params.ApprovalID, params.ToolCallID, params.ToolName, params.TurnID, params.Presentation, params.Options)
	approvalID := f.approvalID
	presentation, options := f.presentation, f.options
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
	metadata := approvalMessageMetadata(approvalID, f.turnID, presentation, options, &decision, params.ExpiresAt)
	uiMessage := buildApprovalUIMessage(f, ApprovalPromptStateResponded, approvalPayload, metadata)
	return ApprovalPromptMessage{
		Content: &event.MessageEventContent{
			MsgType:  event.MsgNotice,
			Body:     body,
			Mentions: &event.Mentions{},
		},
		TopLevelExtra: map[string]any{matrixevents.BeeperAIKey: uiMessage},
		Body:          body,
		UIMessage:     uiMessage,
		Presentation:  presentation,
		Options:       options,
	}
}

// buildApprovalUIMessage constructs the UI message map shared by both
// BuildApprovalPromptMessage and BuildApprovalResponsePromptMessage.
func buildApprovalUIMessage(f normalizedPromptFields, state string, approvalPayload map[string]any, metadata map[string]any) map[string]any {
	return map[string]any{
		"id":       f.approvalID,
		"role":     "assistant",
		"metadata": metadata,
		"parts": []map[string]any{{
			"type":       "dynamic-tool",
			"toolName":   f.toolName,
			"toolCallId": f.toolCallID,
			"state":      state,
			"approval":   approvalPayload,
		}},
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

type ApprovalPromptRegistration struct {
	ApprovalID              string
	RoomID                  id.RoomID
	OwnerMXID               id.UserID
	ToolCallID              string
	ToolName                string
	TurnID                  string
	PromptVersion           uint64
	Presentation            ApprovalPromptPresentation
	ExpiresAt               time.Time
	Options                 []ApprovalOption
	ReactionTargetMessageID networkid.MessageID
	PromptMessageID         networkid.MessageID
	PromptSenderID          networkid.UserID
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

func normalizeApprovalOptions(options []ApprovalOption, fallback []ApprovalOption) []ApprovalOption {
	allowAlways := true
	switch {
	case len(options) > 0:
		allowAlways = false
		for _, option := range options {
			if strings.TrimSpace(option.ID) == "allow_always" || option.Always {
				allowAlways = true
				break
			}
		}
	case len(fallback) > 0:
		allowAlways = false
		for _, option := range fallback {
			if strings.TrimSpace(option.ID) == "allow_always" || option.Always {
				allowAlways = true
				break
			}
		}
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
	if strings.HasPrefix(key, "approval.") {
		return true
	}
	for _, option := range ApprovalPromptOptions(true) {
		for _, optionKey := range option.allKeys() {
			if key == optionKey {
				return true
			}
		}
	}
	return false
}
