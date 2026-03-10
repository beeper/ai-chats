package bridgeadapter

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"go.mau.fi/util/variationselector"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/matrixevents"
)

const ApprovalDecisionKey = "com.beeper.ai.approval_decision"

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
	return slices.Clone(keys)
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
	resolved := normalizeApprovalOptions(options)
	actionHints := make([]string, 0, len(resolved))
	for _, opt := range resolved {
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

type ApprovalPromptStore struct {
	mu         sync.Mutex
	byApproval map[string]*ApprovalPromptRegistration
	byEventID  map[id.EventID]string
}

func NewApprovalPromptStore() *ApprovalPromptStore {
	return &ApprovalPromptStore{
		byApproval: make(map[string]*ApprovalPromptRegistration),
		byEventID:  make(map[id.EventID]string),
	}
}

func (s *ApprovalPromptStore) Register(reg ApprovalPromptRegistration) {
	if s == nil {
		return
	}
	reg.ApprovalID = strings.TrimSpace(reg.ApprovalID)
	if reg.ApprovalID == "" {
		return
	}
	reg.ToolCallID = strings.TrimSpace(reg.ToolCallID)
	reg.ToolName = strings.TrimSpace(reg.ToolName)
	reg.TurnID = strings.TrimSpace(reg.TurnID)
	reg.Options = normalizeApprovalOptions(reg.Options)

	s.mu.Lock()
	defer s.mu.Unlock()
	if prev := s.byApproval[reg.ApprovalID]; prev != nil && prev.PromptEventID != "" {
		delete(s.byEventID, prev.PromptEventID)
	}
	copyReg := reg
	s.byApproval[reg.ApprovalID] = &copyReg
	if reg.PromptEventID != "" {
		s.byEventID[reg.PromptEventID] = reg.ApprovalID
	}
}

func (s *ApprovalPromptStore) BindPromptEvent(approvalID string, eventID id.EventID) bool {
	if s == nil {
		return false
	}
	approvalID = strings.TrimSpace(approvalID)
	eventID = id.EventID(strings.TrimSpace(eventID.String()))
	if approvalID == "" || eventID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.byApproval[approvalID]
	if entry == nil {
		return false
	}
	if entry.PromptEventID != "" {
		delete(s.byEventID, entry.PromptEventID)
	}
	entry.PromptEventID = eventID
	s.byEventID[eventID] = approvalID
	return true
}

func (s *ApprovalPromptStore) Drop(approvalID string) {
	if s == nil {
		return
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.byApproval[approvalID]
	if entry != nil && entry.PromptEventID != "" {
		delete(s.byEventID, entry.PromptEventID)
	}
	delete(s.byApproval, approvalID)
}

type ApprovalPromptReactionMatch struct {
	KnownPrompt   bool
	ShouldResolve bool
	ApprovalID    string
	Decision      ApprovalDecisionPayload
	RejectReason  string
	Prompt        ApprovalPromptRegistration
}

func (s *ApprovalPromptStore) MatchReaction(targetEventID id.EventID, sender id.UserID, key string, now time.Time) ApprovalPromptReactionMatch {
	targetEventID = id.EventID(strings.TrimSpace(targetEventID.String()))
	sender = id.UserID(strings.TrimSpace(sender.String()))
	key = normalizeReactionKey(key)
	if s == nil || targetEventID == "" || key == "" {
		return ApprovalPromptReactionMatch{}
	}
	s.mu.Lock()
	approvalID := s.byEventID[targetEventID]
	entry := s.byApproval[approvalID]
	if entry == nil {
		s.mu.Unlock()
		return ApprovalPromptReactionMatch{}
	}
	promptCopy := *entry
	promptCopy.Options = slices.Clone(entry.Options)
	s.mu.Unlock()

	match := ApprovalPromptReactionMatch{
		KnownPrompt: true,
		ApprovalID:  approvalID,
		Prompt:      promptCopy,
	}
	if promptCopy.OwnerMXID != "" && sender != promptCopy.OwnerMXID {
		match.RejectReason = "only_owner"
		return match
	}
	if !promptCopy.ExpiresAt.IsZero() && !now.IsZero() && now.After(promptCopy.ExpiresAt) {
		match.RejectReason = "expired"
		return match
	}
	for _, opt := range promptCopy.Options {
		for _, optKey := range opt.allKeys() {
			if key != optKey {
				continue
			}
			match.ShouldResolve = true
			match.Decision = ApprovalDecisionPayload{
				ApprovalID: promptCopy.ApprovalID,
				Approved:   opt.Approved,
				Always:     opt.Always,
				Reason:     opt.decisionReason(),
			}
			return match
		}
	}
	match.RejectReason = "invalid_option"
	return match
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
