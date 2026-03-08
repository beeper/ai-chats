package connector

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/connector/msgconv"
)

var (
	_ bridgev2.RemoteMessage                  = (*OpenAIRemoteMessage)(nil)
	_ bridgev2.RemoteEventWithTimestamp       = (*OpenAIRemoteMessage)(nil)
	_ bridgev2.RemoteEventWithStreamOrder     = (*OpenAIRemoteMessage)(nil)
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

func (m *OpenAIRemoteMessage) GetStreamOrder() int64 {
	return m.GetTimestamp().UnixMilli()
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
	if m.Metadata != nil && m.Metadata.Body == "" {
		m.Metadata.Body = m.Content
	}

	// Prefer the message metadata model when present.
	model := ""
	if m.Metadata != nil && m.Metadata.Model != "" {
		model = m.Metadata.Model
	}

	var thinkingContent string
	var toolCalls []ToolCallMetadata
	params := msgconv.UIMessageMetadataParams{Model: model, IncludeUsage: true}
	if m.Metadata != nil {
		thinkingContent = m.Metadata.ThinkingContent
		toolCalls = m.Metadata.ToolCalls
		params.TurnID = m.Metadata.TurnID
		params.AgentID = m.Metadata.AgentID
		params.FinishReason = m.Metadata.FinishReason
		params.CompletionID = m.Metadata.CompletionID
		params.PromptTokens = m.Metadata.PromptTokens
		params.CompletionTokens = m.Metadata.CompletionTokens
		params.ReasoningTokens = m.Metadata.ReasoningTokens
		params.StartedAtMs = m.Metadata.StartedAtMs
		params.FirstTokenAtMs = m.Metadata.FirstTokenAtMs
		params.CompletedAtMs = m.Metadata.CompletedAtMs
	}

	return msgconv.ConvertAIResponse(msgconv.AIResponseParams{
		Content:          m.Content,
		FormattedContent: m.FormattedContent,
		ReplyToEventID:   m.ReplyToEventID,
		Metadata:         params,
		ThinkingContent:  thinkingContent,
		ToolCalls:        toolCalls,
		PortalModel:      model,
		ProviderToolType: string(ToolTypeProvider),
		SuccessStatus:    string(ResultStatusSuccess),
		DeniedStatus:     string(ResultStatusDenied),
		DBMetadata:       m.Metadata,
	})
}
