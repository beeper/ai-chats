package connector

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

var (
	_ bridgev2.RemoteMessage                  = (*OpenAIRemoteMessage)(nil)
	_ bridgev2.RemoteEventWithTimestamp       = (*OpenAIRemoteMessage)(nil)
	_ bridgev2.RemoteMessageWithTransactionID = (*OpenAIRemoteMessage)(nil)
)

// OpenAIRemoteMessage represents a GPT answer that should be bridged to Matrix.
type OpenAIRemoteMessage struct {
	PortalKey networkid.PortalKey
	ID        networkid.MessageID
	Sender    bridgev2.EventSender
	Content   string
	Timestamp time.Time
	Metadata  *MessageMetadata

	FormattedContent string
	ReplyToEventID   id.EventID
	ToolCallEventIDs []string
	ImageEventIDs    []string
}

func (m *OpenAIRemoteMessage) GetType() bridgev2.RemoteEventType {
	return bridgev2.RemoteEventMessage
}

func (m *OpenAIRemoteMessage) GetPortalKey() networkid.PortalKey {
	return m.PortalKey
}

func (m *OpenAIRemoteMessage) AddLogContext(c zerolog.Context) zerolog.Context {
	return c.Str("openai_message_id", string(m.ID))
}

func (m *OpenAIRemoteMessage) GetSender() bridgev2.EventSender {
	return m.Sender
}

func (m *OpenAIRemoteMessage) GetID() networkid.MessageID {
	return m.ID
}

func (m *OpenAIRemoteMessage) GetTimestamp() time.Time {
	if m.Timestamp.IsZero() {
		return time.Now()
	}
	return m.Timestamp
}

// GetTransactionID implements RemoteMessageWithTransactionID
func (m *OpenAIRemoteMessage) GetTransactionID() networkid.TransactionID {
	// Use completion ID as transaction ID for deduplication
	if m.Metadata != nil && m.Metadata.CompletionID != "" {
		return networkid.TransactionID("completion-" + m.Metadata.CompletionID)
	}
	return ""
}

func (m *OpenAIRemoteMessage) ConvertMessage(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI) (*bridgev2.ConvertedMessage, error) {
	content := &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    m.Content,
	}

	// Add formatted content if available
	if m.FormattedContent != "" {
		content.Format = event.FormatHTML
		content.FormattedBody = m.FormattedContent
	}

	if m.Metadata != nil && m.Metadata.Body == "" {
		m.Metadata.Body = m.Content
	}

	// Build the new com.beeper.ai nested structure
	extra := map[string]any{}

	// Get model from metadata or portal fallback
	model := ""
	if m.Metadata != nil && m.Metadata.Model != "" {
		model = m.Metadata.Model
	} else if portalMeta, ok := portal.Metadata.(*PortalMetadata); ok && portalMeta.Model != "" {
		model = portalMeta.Model
	}

	// Build AI SDK UIMessage payload
	uiParts := make([]map[string]any, 0, 2)
	if m.Metadata != nil && m.Metadata.ThinkingContent != "" {
		uiParts = append(uiParts, map[string]any{
			"type":  "reasoning",
			"text":  m.Metadata.ThinkingContent,
			"state": "done",
		})
	}
	if m.Content != "" {
		uiParts = append(uiParts, map[string]any{
			"type":  "text",
			"text":  m.Content,
			"state": "done",
		})
	}
	if m.Metadata != nil && len(m.Metadata.ToolCalls) > 0 {
		for _, tc := range m.Metadata.ToolCalls {
			toolPart := map[string]any{
				"type":       "dynamic-tool",
				"toolName":   tc.ToolName,
				"toolCallId": tc.CallID,
				"input":      tc.Input,
			}
			if tc.ToolType == string(ToolTypeProvider) {
				toolPart["providerExecuted"] = true
			}
			if tc.ResultStatus == string(ResultStatusSuccess) {
				toolPart["state"] = "output-available"
				toolPart["output"] = tc.Output
			} else {
				toolPart["state"] = "output-error"
				if tc.ErrorMessage != "" {
					toolPart["errorText"] = tc.ErrorMessage
				} else if result, ok := tc.Output["result"].(string); ok && result != "" {
					toolPart["errorText"] = result
				}
			}
			uiParts = append(uiParts, toolPart)
		}
	}

	uiMetadata := map[string]any{}
	if m.Metadata != nil {
		if m.Metadata.TurnID != "" {
			uiMetadata["turn_id"] = m.Metadata.TurnID
		}
		if m.Metadata.AgentID != "" {
			uiMetadata["agent_id"] = m.Metadata.AgentID
		}
		if model != "" {
			uiMetadata["model"] = model
		}
		if m.Metadata.FinishReason != "" {
			uiMetadata["finish_reason"] = mapFinishReason(m.Metadata.FinishReason)
		}
		if m.Metadata.PromptTokens > 0 || m.Metadata.CompletionTokens > 0 || m.Metadata.ReasoningTokens > 0 {
			uiMetadata["usage"] = map[string]any{
				"prompt_tokens":     m.Metadata.PromptTokens,
				"completion_tokens": m.Metadata.CompletionTokens,
				"reasoning_tokens":  m.Metadata.ReasoningTokens,
			}
		}
		timing := map[string]any{}
		if m.Metadata.StartedAtMs > 0 {
			timing["started_at"] = m.Metadata.StartedAtMs
		}
		if m.Metadata.FirstTokenAtMs > 0 {
			timing["first_token_at"] = m.Metadata.FirstTokenAtMs
		}
		if m.Metadata.CompletedAtMs > 0 {
			timing["completed_at"] = m.Metadata.CompletedAtMs
		}
		if len(timing) > 0 {
			uiMetadata["timing"] = timing
		}
		if m.Metadata.CompletionID != "" {
			uiMetadata["completion_id"] = m.Metadata.CompletionID
		}
	}

	turnID := ""
	if m.Metadata != nil {
		turnID = m.Metadata.TurnID
	}

	uiMessage := map[string]any{
		"id":       turnID,
		"role":     "assistant",
		"metadata": uiMetadata,
		"parts":    uiParts,
	}
	extra[BeeperAIKey] = uiMessage

	// Build m.relates_to for threading if we have a reply target
	if m.ReplyToEventID != "" {
		extra["m.relates_to"] = map[string]any{
			"rel_type":        RelThread,
			"event_id":        m.ReplyToEventID.String(),
			"is_falling_back": true,
			"m.in_reply_to": map[string]any{
				"event_id": m.ReplyToEventID.String(),
			},
		}
	}

	part := &bridgev2.ConvertedMessagePart{
		ID:         networkid.PartID("0"),
		Type:       event.EventMessage,
		Content:    content,
		Extra:      extra,
		DBMetadata: m.Metadata,
	}
	return &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{part},
	}, nil
}
