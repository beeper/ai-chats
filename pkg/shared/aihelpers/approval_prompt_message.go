package aihelpers

import (
	"strings"
	"time"

	"go.mau.fi/util/variationselector"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-chats/pkg/matrixevents"
)

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
