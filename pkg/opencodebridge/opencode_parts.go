package opencodebridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/matrixevents"
	"github.com/beeper/ai-bridge/pkg/opencode"
)

type openCodePartKind int

const (
	openCodePartKindMessage openCodePartKind = iota
	openCodePartKindToolCall
	openCodePartKindToolResult
)

type openCodePartEvent struct {
	InstanceID string
	Part       opencode.Part
	Kind       openCodePartKind
	Status     string
}

func (b *Bridge) emitOpenCodePart(ctx context.Context, portal *bridgev2.Portal, instanceID string, part opencode.Part, fromMe bool) {
	if portal == nil || part.ID == "" {
		return
	}
	remote := &simplevent.Message[openCodePartEvent]{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventMessage,
			PortalKey: portal.PortalKey,
			Sender:    b.opencodeSender(instanceID, fromMe),
		},
		ID:                 opencodePartMessageID(part.ID),
		Data:               openCodePartEvent{InstanceID: instanceID, Part: part, Kind: openCodePartKindMessage},
		ConvertMessageFunc: b.convertOpenCodePartMessage,
	}
	b.queueRemoteEvent(remote)
}

func (b *Bridge) emitOpenCodePartEdit(ctx context.Context, portal *bridgev2.Portal, instanceID string, part opencode.Part, fromMe bool) {
	if portal == nil || part.ID == "" {
		return
	}
	remote := &simplevent.Message[openCodePartEvent]{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventEdit,
			PortalKey: portal.PortalKey,
			Sender:    b.opencodeSender(instanceID, fromMe),
		},
		TargetMessage:   opencodePartMessageID(part.ID),
		Data:            openCodePartEvent{InstanceID: instanceID, Part: part, Kind: openCodePartKindMessage},
		ConvertEditFunc: b.convertOpenCodePartEdit,
	}
	b.queueRemoteEvent(remote)
}

func (b *Bridge) emitOpenCodeToolCall(ctx context.Context, portal *bridgev2.Portal, instanceID string, part opencode.Part, fromMe bool, status string) {
	if portal == nil || part.ID == "" {
		return
	}
	remote := &simplevent.Message[openCodePartEvent]{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventMessage,
			PortalKey: portal.PortalKey,
			Sender:    b.opencodeSender(instanceID, fromMe),
		},
		ID:                 opencodeToolCallMessageID(part.ID),
		Data:               openCodePartEvent{InstanceID: instanceID, Part: part, Kind: openCodePartKindToolCall, Status: status},
		ConvertMessageFunc: b.convertOpenCodePartMessage,
	}
	b.queueRemoteEvent(remote)
}

func (b *Bridge) emitOpenCodeToolResult(ctx context.Context, portal *bridgev2.Portal, instanceID string, part opencode.Part, fromMe bool, status string) {
	if portal == nil || part.ID == "" {
		return
	}
	remote := &simplevent.Message[openCodePartEvent]{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventMessage,
			PortalKey: portal.PortalKey,
			Sender:    b.opencodeSender(instanceID, fromMe),
		},
		ID:                 opencodeToolResultMessageID(part.ID),
		Data:               openCodePartEvent{InstanceID: instanceID, Part: part, Kind: openCodePartKindToolResult, Status: status},
		ConvertMessageFunc: b.convertOpenCodePartMessage,
	}
	b.queueRemoteEvent(remote)
}

func (b *Bridge) emitOpenCodeToolStatusReaction(ctx context.Context, portal *bridgev2.Portal, instanceID string, part opencode.Part, fromMe bool, reaction string) {
	if portal == nil || part.ID == "" || reaction == "" {
		return
	}
	remote := &simplevent.Reaction{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventReaction,
			PortalKey: portal.PortalKey,
			Sender:    b.opencodeSender(instanceID, fromMe),
		},
		TargetMessage: opencodeToolCallMessageID(part.ID),
		Emoji:         reaction,
		EmojiID:       networkid.EmojiID(reaction),
	}
	b.queueRemoteEvent(remote)
}

func (b *Bridge) emitOpenCodeToolStatusReactionRemove(ctx context.Context, portal *bridgev2.Portal, instanceID string, part opencode.Part, fromMe bool, reaction string) {
	if portal == nil || part.ID == "" || reaction == "" {
		return
	}
	remote := &simplevent.Reaction{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventReactionRemove,
			PortalKey: portal.PortalKey,
			Sender:    b.opencodeSender(instanceID, fromMe),
		},
		TargetMessage: opencodeToolCallMessageID(part.ID),
		EmojiID:       networkid.EmojiID(reaction),
	}
	b.queueRemoteEvent(remote)
}

func (b *Bridge) convertOpenCodePartMessage(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, data openCodePartEvent) (*bridgev2.ConvertedMessage, error) {
	cmp, err := b.buildOpenCodeConvertedPart(ctx, portal, intent, data)
	if err != nil {
		return nil, err
	}
	if cmp == nil {
		return nil, bridgev2.ErrIgnoringRemoteEvent
	}
	return &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{cmp},
	}, nil
}

func (b *Bridge) convertOpenCodePartEdit(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, existing []*database.Message, data openCodePartEvent) (*bridgev2.ConvertedEdit, error) {
	if len(existing) == 0 {
		return nil, bridgev2.ErrIgnoringRemoteEvent
	}
	if data.Part.Type != "text" && data.Part.Type != "reasoning" {
		return nil, bridgev2.ErrIgnoringRemoteEvent
	}
	cmp, err := b.buildOpenCodeConvertedPart(ctx, portal, intent, data)
	if err != nil {
		return nil, err
	}
	if cmp == nil {
		return nil, bridgev2.ErrIgnoringRemoteEvent
	}
	return &bridgev2.ConvertedEdit{
		ModifiedParts: []*bridgev2.ConvertedEditPart{cmp.ToEditPart(existing[0])},
	}, nil
}

func (b *Bridge) buildOpenCodeConvertedPart(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, data openCodePartEvent) (*bridgev2.ConvertedMessagePart, error) {
	part := data.Part
	switch data.Kind {
	case openCodePartKindToolCall:
		content, extra := buildOpenCodeToolCallContent(part, data.Status)
		return &bridgev2.ConvertedMessagePart{
			ID:      networkid.PartID("0"),
			Type:    matrixevents.ToolCallEventType,
			Content: content,
			Extra:   extra,
		}, nil
	case openCodePartKindToolResult:
		content, extra := buildOpenCodeToolResultContent(part, data.Status)
		return &bridgev2.ConvertedMessagePart{
			ID:      networkid.PartID("0"),
			Type:    matrixevents.ToolResultEventType,
			Content: content,
			Extra:   extra,
		}, nil
	default:
		content, extra, err := b.buildOpenCodePartContent(ctx, portal, intent, part)
		if err != nil {
			return nil, err
		}
		if content == nil {
			return nil, bridgev2.ErrIgnoringRemoteEvent
		}
		return &bridgev2.ConvertedMessagePart{
			ID:      networkid.PartID("0"),
			Type:    event.EventMessage,
			Content: content,
			Extra:   extra,
		}, nil
	}
}

func (b *Bridge) buildOpenCodePartContent(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, part opencode.Part) (*event.MessageEventContent, map[string]any, error) {
	switch part.Type {
	case "text":
		body := strings.TrimSpace(part.Text)
		if body == "" {
			return nil, nil, nil
		}
		return &event.MessageEventContent{MsgType: event.MsgText, Body: body}, nil, nil
	case "reasoning":
		body := strings.TrimSpace(part.Text)
		if body == "" {
			return nil, nil, nil
		}
		return &event.MessageEventContent{MsgType: event.MsgNotice, Body: "Reasoning:\n" + body}, nil, nil
	case "file":
		content, err := b.buildOpenCodeFileContent(ctx, portal, intent, part)
		if err != nil {
			body := "OpenCode file unavailable"
			if part.URL != "" {
				body = body + ": " + part.URL
			}
			return &event.MessageEventContent{MsgType: event.MsgNotice, Body: body}, nil, nil
		}
		return content, nil, nil
	case "patch":
		body := "Patch " + strings.TrimSpace(part.Hash)
		if len(part.Files) > 0 {
			body = strings.TrimSpace(body + "\nFiles: " + strings.Join(part.Files, ", "))
		}
		return &event.MessageEventContent{MsgType: event.MsgNotice, Body: body}, nil, nil
	case "snapshot":
		body := strings.TrimSpace(part.Snapshot)
		if body == "" {
			body = "Snapshot saved"
		} else {
			body = "Snapshot:\n" + truncateOpenCodeText(body, 4000)
		}
		return &event.MessageEventContent{MsgType: event.MsgNotice, Body: body}, nil, nil
	case "step-start":
		body := "Step started"
		if strings.TrimSpace(part.Snapshot) != "" {
			body = body + ": " + truncateOpenCodeText(strings.TrimSpace(part.Snapshot), 200)
		}
		return &event.MessageEventContent{MsgType: event.MsgNotice, Body: body}, nil, nil
	case "step-finish":
		body := "Step finished"
		reason := strings.TrimSpace(part.Reason)
		if reason != "" {
			body = body + ": " + reason
		}
		if part.Cost > 0 {
			body = body + fmt.Sprintf(" (cost %.4f)", part.Cost)
		}
		return &event.MessageEventContent{MsgType: event.MsgNotice, Body: body}, nil, nil
	case "agent":
		name := strings.TrimSpace(part.Name)
		if name == "" {
			name = "(unknown)"
		}
		return &event.MessageEventContent{MsgType: event.MsgNotice, Body: "Agent: " + name}, nil, nil
	case "subtask":
		desc := strings.TrimSpace(part.Description)
		prompt := strings.TrimSpace(part.Prompt)
		body := "Subtask"
		if desc != "" {
			body = body + ": " + desc
		} else if prompt != "" {
			body = body + ": " + truncateOpenCodeText(prompt, 300)
		}
		if part.Agent != "" {
			body = body + " (agent: " + part.Agent + ")"
		}
		return &event.MessageEventContent{MsgType: event.MsgNotice, Body: body}, nil, nil
	case "retry":
		body := fmt.Sprintf("Retry attempt %d", part.Attempt)
		if len(part.Error) > 0 {
			body = body + ": " + truncateOpenCodeText(string(part.Error), 300)
		}
		return &event.MessageEventContent{MsgType: event.MsgNotice, Body: body}, nil, nil
	case "compaction":
		body := fmt.Sprintf("Compaction (auto: %t)", part.Auto)
		return &event.MessageEventContent{MsgType: event.MsgNotice, Body: body}, nil, nil
	default:
		body := "OpenCode part: " + part.Type
		return &event.MessageEventContent{MsgType: event.MsgNotice, Body: body}, nil, nil
	}
}

func buildOpenCodeToolCallContent(part opencode.Part, status string) (*event.MessageEventContent, map[string]any) {
	toolName := strings.TrimSpace(part.Tool)
	if toolName == "" {
		toolName = "tool"
	}
	callID := strings.TrimSpace(part.CallID)
	if callID == "" {
		callID = part.ID
	}
	toolStatus := opencodeToolStatus(firstNonEmptyString(status, opencodePartStateStatus(part)))
	call := &ToolCallData{
		CallID:   callID,
		ToolName: toolName,
		ToolType: ToolTypeBuiltin,
		Status:   toolStatus,
	}
	if part.State != nil && len(part.State.Input) > 0 {
		call.Input = part.State.Input
	}
	call.Display = &ToolDisplay{Title: toolDisplayTitle(toolName), Collapsed: false}
	if part.State != nil && part.State.Time != nil {
		call.Timing = &TimingInfo{StartedAt: int64(part.State.Time.Start)}
	}
	body := fmt.Sprintf("Calling %s...", toolDisplayTitle(toolName))
	content := &event.MessageEventContent{MsgType: event.MsgNotice, Body: body}
	extra := map[string]any{BeeperAIToolCallKey: call}
	return content, extra
}

func buildOpenCodeToolResultContent(part opencode.Part, status string) (*event.MessageEventContent, map[string]any) {
	toolName := strings.TrimSpace(part.Tool)
	if toolName == "" {
		toolName = "tool"
	}
	callID := strings.TrimSpace(part.CallID)
	if callID == "" {
		callID = part.ID
	}
	stateStatus := firstNonEmptyString(status, opencodePartStateStatus(part))
	resultStatus := opencodeResultStatus(stateStatus)

	outputText := ""
	if part.State != nil {
		outputText = strings.TrimSpace(part.State.Output)
		if outputText == "" {
			outputText = strings.TrimSpace(part.State.Error)
		}
	}
	body := summarizeOpenCodeToolOutput(toolName, outputText)
	output := parseOpenCodeToolOutput(outputText)
	if len(output) == 0 && stateStatus == "error" {
		output = map[string]any{"error": outputText}
	}
	result := &ToolResultData{
		CallID:   callID,
		ToolName: toolName,
		Status:   resultStatus,
		Output:   output,
		Display: &ToolResultDisplay{
			Expandable:      len(outputText) > 200,
			DefaultExpanded: len(outputText) <= 500,
		},
	}
	content := &event.MessageEventContent{MsgType: event.MsgNotice, Body: body}
	extra := map[string]any{BeeperAIToolResultKey: result}
	return content, extra
}

func parseOpenCodeToolOutput(output string) map[string]any {
	if strings.TrimSpace(output) == "" {
		return nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(output), &parsed); err == nil {
		if obj, ok := parsed.(map[string]any); ok {
			return obj
		}
	}
	return map[string]any{"output": output}
}

func summarizeOpenCodeToolOutput(toolName, output string) string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return toolDisplayTitle(toolName) + " completed"
	}
	if len(trimmed) > 200 {
		trimmed = trimmed[:200] + "..."
	}
	return trimmed
}

func opencodeToolStatus(status string) ToolStatus {
	switch status {
	case "pending":
		return ToolStatusPending
	case "running":
		return ToolStatusRunning
	case "completed":
		return ToolStatusCompleted
	case "error":
		return ToolStatusFailed
	default:
		return ToolStatusRunning
	}
}

func opencodeResultStatus(status string) ResultStatus {
	switch status {
	case "error":
		return ResultStatusError
	case "completed":
		return ResultStatusSuccess
	default:
		return ResultStatusSuccess
	}
}

func opencodeToolStatusReaction(part opencode.Part, status string) string {
	toolName := strings.TrimSpace(part.Tool)
	if toolName == "" {
		toolName = "tool"
	}
	label := toolDisplayTitle(toolName)
	switch status {
	case "pending":
		return "pending: " + label
	case "running":
		return "running: " + label
	default:
		return ""
	}
}

func truncateOpenCodeText(text string, max int) string {
	if max <= 0 || len(text) <= max {
		return text
	}
	return text[:max] + "..."
}

func opencodePartStateStatus(part opencode.Part) string {
	if part.State == nil {
		return ""
	}
	return part.State.Status
}
