package msgconv

import (
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
	"github.com/beeper/ai-bridge/pkg/matrixevents"
)

// ToolCallPart builds a single AI SDK UIMessage dynamic-tool part from tool call metadata.
func ToolCallPart(tc bridgeadapter.ToolCallMetadata, providerToolType string, successStatus, deniedStatus string) map[string]any {
	part := map[string]any{
		"type":       "dynamic-tool",
		"toolName":   tc.ToolName,
		"toolCallId": tc.CallID,
		"input":      tc.Input,
	}
	if tc.ToolType == providerToolType {
		part["providerExecuted"] = true
	}
	switch tc.ResultStatus {
	case successStatus:
		part["state"] = "output-available"
		part["output"] = tc.Output
	case deniedStatus:
		part["state"] = "output-denied"
		part["errorText"] = "Denied by user"
	default:
		part["state"] = "output-error"
		if tc.ErrorMessage != "" {
			part["errorText"] = tc.ErrorMessage
		} else if result, ok := tc.Output["result"].(string); ok && result != "" {
			part["errorText"] = result
		}
	}
	return part
}

// ToolCallParts builds AI SDK UIMessage dynamic-tool parts from a list of tool call metadata.
func ToolCallParts(toolCalls []bridgeadapter.ToolCallMetadata, providerToolType, successStatus, deniedStatus string) []map[string]any {
	if len(toolCalls) == 0 {
		return nil
	}
	parts := make([]map[string]any, 0, len(toolCalls))
	for _, tc := range toolCalls {
		parts = append(parts, ToolCallPart(tc, providerToolType, successStatus, deniedStatus))
	}
	return parts
}

// UIMessageMetadataParams contains parameters for building UI message metadata.
type UIMessageMetadataParams struct {
	TurnID           string
	AgentID          string
	Model            string
	FinishReason     string
	CompletionID     string
	PromptTokens     int64
	CompletionTokens int64
	ReasoningTokens  int64
	TotalTokens      int64
	StartedAtMs      int64
	FirstTokenAtMs   int64
	CompletedAtMs    int64
	IncludeUsage     bool
}

// BuildUIMessageMetadata builds the metadata map for a com.beeper.ai UIMessage.
func BuildUIMessageMetadata(p UIMessageMetadataParams) map[string]any {
	metadata := map[string]any{}
	if p.TurnID != "" {
		metadata["turn_id"] = p.TurnID
	}
	if p.AgentID != "" {
		metadata["agent_id"] = p.AgentID
	}
	if p.Model != "" {
		metadata["model"] = p.Model
	}
	if p.FinishReason != "" {
		metadata["finish_reason"] = MapFinishReason(p.FinishReason)
	}
	if p.CompletionID != "" {
		metadata["completion_id"] = p.CompletionID
	}
	if p.IncludeUsage && (p.PromptTokens > 0 || p.CompletionTokens > 0 || p.ReasoningTokens > 0) {
		usage := map[string]any{
			"prompt_tokens":     p.PromptTokens,
			"completion_tokens": p.CompletionTokens,
			"reasoning_tokens":  p.ReasoningTokens,
		}
		if p.TotalTokens > 0 {
			usage["total_tokens"] = p.TotalTokens
		}
		metadata["usage"] = usage
	}
	if p.IncludeUsage {
		timing := map[string]any{}
		if p.StartedAtMs > 0 {
			timing["started_at"] = p.StartedAtMs
		}
		if p.FirstTokenAtMs > 0 {
			timing["first_token_at"] = p.FirstTokenAtMs
		}
		if p.CompletedAtMs > 0 {
			timing["completed_at"] = p.CompletedAtMs
		}
		if len(timing) > 0 {
			metadata["timing"] = timing
		}
	}
	return metadata
}

// UIMessageParams contains parameters for building a full com.beeper.ai UIMessage.
type UIMessageParams struct {
	TurnID     string
	Role       string // "assistant", "user"
	Metadata   map[string]any
	Parts      []map[string]any
	SourceURLs []map[string]any // Optional source-url and source-document parts
	FileParts  []map[string]any // Optional generated file parts
}

// BuildUIMessage builds the complete com.beeper.ai UIMessage payload.
func BuildUIMessage(p UIMessageParams) map[string]any {
	role := p.Role
	if role == "" {
		role = "assistant"
	}
	allParts := p.Parts
	if len(p.SourceURLs) > 0 {
		allParts = append(allParts, p.SourceURLs...)
	}
	if len(p.FileParts) > 0 {
		allParts = append(allParts, p.FileParts...)
	}
	msg := map[string]any{
		"id":    p.TurnID,
		"role":  role,
		"parts": allParts,
	}
	if len(p.Metadata) > 0 {
		msg["metadata"] = p.Metadata
	}
	return msg
}

// ContentParts builds the standard text + reasoning parts for a UIMessage.
func ContentParts(textContent, reasoningContent string) []map[string]any {
	parts := make([]map[string]any, 0, 2)
	if reasoningContent != "" {
		parts = append(parts, map[string]any{
			"type":  "reasoning",
			"text":  reasoningContent,
			"state": "done",
		})
	}
	if textContent != "" {
		parts = append(parts, map[string]any{
			"type":  "text",
			"text":  textContent,
			"state": "done",
		})
	}
	return parts
}

// RelatesToThread builds a m.relates_to payload for threading with fallback reply.
func RelatesToThread(threadRoot id.EventID, replyTo id.EventID) map[string]any {
	if threadRoot == "" {
		if replyTo == "" {
			return nil
		}
		return map[string]any{
			"m.in_reply_to": map[string]any{
				"event_id": replyTo.String(),
			},
		}
	}
	rel := map[string]any{
		"rel_type":        matrixevents.RelThread,
		"event_id":        threadRoot.String(),
		"is_falling_back": true,
		"m.in_reply_to": map[string]any{
			"event_id": replyTo.String(),
		},
	}
	return rel
}

// RelatesToReplace builds a m.relates_to payload for an edit (m.replace) event.
func RelatesToReplace(initialEventID id.EventID, replyTo id.EventID) map[string]any {
	if initialEventID == "" {
		return nil
	}
	rel := map[string]any{
		"rel_type": matrixevents.RelReplace,
		"event_id": initialEventID.String(),
	}
	if replyTo != "" {
		rel["m.in_reply_to"] = map[string]any{
			"event_id": replyTo.String(),
		}
	}
	return rel
}

// FinalEditContentParams contains parameters for building a streaming final edit event.
type FinalEditContentParams struct {
	Rendered       event.MessageEventContent
	RelatesTo      map[string]any
	UIMessage      map[string]any
	LinkPreviews   []map[string]any
	DontShowEdited bool
}

// BuildFinalEditContent builds the event content for a streaming final edit (m.replace).
func BuildFinalEditContent(p FinalEditContentParams) *event.Content {
	raw := map[string]any{
		"msgtype":        event.MsgText,
		"body":           "* AI response",
		"format":         p.Rendered.Format,
		"formatted_body": "* AI response",
		"m.new_content": map[string]any{
			"msgtype":        event.MsgText,
			"body":           p.Rendered.Body,
			"format":         p.Rendered.Format,
			"formatted_body": p.Rendered.FormattedBody,
			"m.mentions":     map[string]any{},
		},
		matrixevents.BeeperAIKey: p.UIMessage,
		"m.mentions":             map[string]any{},
	}
	if p.RelatesTo != nil {
		raw["m.relates_to"] = p.RelatesTo
	}
	if p.DontShowEdited {
		raw["com.beeper.dont_render_edited"] = true
	}
	if len(p.LinkPreviews) > 0 {
		raw["com.beeper.linkpreviews"] = p.LinkPreviews
	}
	return &event.Content{Raw: raw}
}

// PlainMessageContentParams contains parameters for building a plain text message.
type PlainMessageContentParams struct {
	Text         string
	RelatesTo    map[string]any
	UIMessage    map[string]any
	LinkPreviews []map[string]any
}

// BuildPlainMessageContent builds event content for a plain assistant text message.
func BuildPlainMessageContent(p PlainMessageContentParams) *event.Content {
	rendered := format.RenderMarkdown(p.Text, true, true)
	raw := map[string]any{
		"msgtype":        event.MsgText,
		"body":           rendered.Body,
		"format":         rendered.Format,
		"formatted_body": rendered.FormattedBody,
		"m.mentions":     map[string]any{},
	}
	if p.RelatesTo != nil {
		raw["m.relates_to"] = p.RelatesTo
	}
	if p.UIMessage != nil {
		raw[matrixevents.BeeperAIKey] = p.UIMessage
	}
	if len(p.LinkPreviews) > 0 {
		raw["com.beeper.linkpreviews"] = p.LinkPreviews
	}
	return &event.Content{Raw: raw}
}

// AIResponseParams contains parameters for converting an AI response to a ConvertedMessage.
// Used by both OpenAIRemoteMessage.ConvertMessage and new AIRemoteMessage types.
type AIResponseParams struct {
	Content          string
	FormattedContent string
	ReplyToEventID   id.EventID
	Metadata         UIMessageMetadataParams
	ThinkingContent  string
	ToolCalls        []bridgeadapter.ToolCallMetadata
	PortalModel      string // Fallback model from portal metadata

	// Tool type constants from the connector package
	ProviderToolType string
	SuccessStatus    string
	DeniedStatus     string

	// DB metadata to attach
	DBMetadata any
}

// ConvertAIResponse converts AI response parameters into a bridgev2 ConvertedMessage.
// This is the shared conversion path for non-streaming final messages.
func ConvertAIResponse(p AIResponseParams) (*bridgev2.ConvertedMessage, error) {
	content := &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    p.Content,
	}
	if p.FormattedContent != "" {
		content.Format = event.FormatHTML
		content.FormattedBody = p.FormattedContent
	}

	model := p.Metadata.Model
	if model == "" {
		model = p.PortalModel
	}
	p.Metadata.Model = model

	// Build parts
	parts := ContentParts(p.Content, p.ThinkingContent)
	if toolParts := ToolCallParts(p.ToolCalls, p.ProviderToolType, p.SuccessStatus, p.DeniedStatus); len(toolParts) > 0 {
		parts = append(parts, toolParts...)
	}

	metadata := BuildUIMessageMetadata(p.Metadata)
	uiMessage := BuildUIMessage(UIMessageParams{
		TurnID:   p.Metadata.TurnID,
		Role:     "assistant",
		Metadata: metadata,
		Parts:    parts,
	})

	extra := map[string]any{
		matrixevents.BeeperAIKey: uiMessage,
	}

	if p.ReplyToEventID != "" {
		extra["m.relates_to"] = RelatesToThread(p.ReplyToEventID, p.ReplyToEventID)
	}

	part := &bridgev2.ConvertedMessagePart{
		ID:         networkid.PartID("0"),
		Type:       event.EventMessage,
		Content:    content,
		Extra:      extra,
		DBMetadata: p.DBMetadata,
	}
	return &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{part},
	}, nil
}

// MapFinishReason normalizes provider-specific finish reasons to standard values.
func MapFinishReason(reason string) string {
	switch strings.TrimSpace(reason) {
	case "stop", "end_turn", "end-turn":
		return "stop"
	case "length", "max_output_tokens":
		return "length"
	case "content_filter", "content-filter":
		return "content-filter"
	case "tool_calls", "tool-calls", "tool_use", "tool-use", "toolUse":
		return "tool-calls"
	case "error":
		return "error"
	default:
		return "other"
	}
}
