package bridgeadapter

import (
	"fmt"
	"strings"
	"time"

	"go.mau.fi/util/variationselector"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/matrixevents"
)

const ApprovalDecisionKey = "com.beeper.ai.approval_decision"

const (
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

func DefaultApprovalOptions() []ApprovalOption {
	return []ApprovalOption{
		{
			ID:       "allow_once",
			Key:      "✅",
			Label:    "Approve once",
			Approved: true,
			Reason:   "allow_once",
		},
		{
			ID:       "allow_always",
			Key:      "🔁",
			Label:    "Always allow",
			Approved: true,
			Always:   true,
			Reason:   "allow_always",
		},
		{
			ID:       "deny",
			Key:      "❌",
			Label:    "Deny",
			Approved: false,
			Reason:   "deny",
		},
	}
}

func BuildApprovalPromptBody(toolName string, options []ApprovalOption) string {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		toolName = "tool"
	}
	actionHints := make([]string, 0, len(options))
	for _, opt := range options {
		key := strings.TrimSpace(opt.Key)
		if key == "" {
			key = strings.TrimSpace(opt.FallbackKey)
		}
		label := strings.TrimSpace(opt.Label)
		if key == "" || label == "" {
			continue
		}
		actionHints = append(actionHints, fmt.Sprintf("%s %s", key, label))
	}
	if len(actionHints) == 0 {
		return fmt.Sprintf("Approval required for %s.", toolName)
	}
	return fmt.Sprintf("Approval required for %s. React with: %s.", toolName, strings.Join(actionHints, ", "))
}

type ApprovalPromptMessageParams struct {
	ApprovalID     string
	ToolCallID     string
	ToolName       string
	TurnID         string
	Body           string
	ReplyToEventID id.EventID
	ExpiresAt      time.Time
	Options        []ApprovalOption
}

type ApprovalPromptMessage struct {
	Body      string
	UIMessage map[string]any
	Raw       map[string]any
	Options   []ApprovalOption
}

func BuildApprovalPromptMessage(params ApprovalPromptMessageParams) ApprovalPromptMessage {
	approvalID := strings.TrimSpace(params.ApprovalID)
	toolCallID := strings.TrimSpace(params.ToolCallID)
	toolName := strings.TrimSpace(params.ToolName)
	turnID := strings.TrimSpace(params.TurnID)
	options := normalizeApprovalOptions(params.Options)
	if toolCallID == "" {
		toolCallID = approvalID
	}
	if toolName == "" {
		toolName = "tool"
	}
	body := strings.TrimSpace(params.Body)
	if body == "" {
		body = BuildApprovalPromptBody(toolName, options)
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
			"state":      "approval-requested",
			"approval": map[string]any{
				"id": approvalID,
			},
		}},
	}
	approvalMeta := map[string]any{
		"kind":       "request",
		"approvalId": approvalID,
		"toolCallId": toolCallID,
		"toolName":   toolName,
		"options":    optionsToRaw(options),
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
		Body:      body,
		UIMessage: uiMessage,
		Raw:       raw,
		Options:   options,
	}
}

type ApprovalPromptRegistration struct {
	ApprovalID    string
	RoomID        id.RoomID
	OwnerMXID     id.UserID
	ToolCallID    string
	ToolName      string
	TurnID        string
	ExpiresAt     time.Time
	Options       []ApprovalOption
	PromptEventID id.EventID
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
