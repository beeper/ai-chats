package opencodebridge

import (
	"context"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/bridges/opencode/opencode"
	"github.com/beeper/ai-bridge/pkg/agents/tools"
	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
)

type openCodePartEvent struct {
	InstanceID string
	Part       opencode.Part
}

func (b *Bridge) emitOpenCodePart(ctx context.Context, portal *bridgev2.Portal, instanceID string, part opencode.Part, fromMe bool) {
	b.emitOpenCodePartEvent(portal, instanceID, part, fromMe, bridgev2.RemoteEventMessage)
}

func (b *Bridge) emitOpenCodePartEdit(ctx context.Context, portal *bridgev2.Portal, instanceID string, part opencode.Part, fromMe bool) {
	b.emitOpenCodePartEvent(portal, instanceID, part, fromMe, bridgev2.RemoteEventEdit)
}

func (b *Bridge) emitOpenCodePartEvent(portal *bridgev2.Portal, instanceID string, part opencode.Part, fromMe bool, eventType bridgev2.RemoteEventType) {
	if portal == nil || part.ID == "" {
		return
	}
	remote := &simplevent.Message[openCodePartEvent]{
		EventMeta: simplevent.EventMeta{
			Type:      eventType,
			PortalKey: portal.PortalKey,
			Sender:    b.opencodeSender(instanceID, fromMe),
		},
		Data: openCodePartEvent{InstanceID: instanceID, Part: part},
	}
	if eventType == bridgev2.RemoteEventMessage {
		remote.ID = opencodePartMessageID(part.ID)
		remote.ConvertMessageFunc = b.convertOpenCodePartMessage
	} else {
		remote.TargetMessage = opencodePartMessageID(part.ID)
		remote.ConvertEditFunc = b.convertOpenCodePartEdit
	}
	b.queueRemoteEvent(remote)
}

func (b *Bridge) convertOpenCodePartMessage(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, data openCodePartEvent) (*bridgev2.ConvertedMessage, error) {
	cmp, err := b.buildOpenCodeConvertedPart(ctx, portal, intent, data.Part)
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
	cmp, err := b.buildOpenCodeConvertedPart(ctx, portal, intent, data.Part)
	if err != nil {
		return nil, err
	}
	if cmp == nil {
		return nil, bridgev2.ErrIgnoringRemoteEvent
	}
	edit := &bridgev2.ConvertedEdit{
		ModifiedParts: []*bridgev2.ConvertedEditPart{cmp.ToEditPart(existing[0])},
	}
	streamtransport.EnsureDontRenderEdited(edit)
	return edit, nil
}

func (b *Bridge) buildOpenCodeConvertedPart(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, part opencode.Part) (*bridgev2.ConvertedMessagePart, error) {
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
				body += ": " + part.URL
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
			body += ": " + truncateOpenCodeText(strings.TrimSpace(part.Snapshot), 200)
		}
		return &event.MessageEventContent{MsgType: event.MsgNotice, Body: body}, nil, nil
	case "step-finish":
		body := "Step finished"
		reason := strings.TrimSpace(part.Reason)
		if reason != "" {
			body += ": " + reason
		}
		if part.Cost > 0 {
			body += fmt.Sprintf(" (cost %.4f)", part.Cost)
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
			body += ": " + desc
		} else if prompt != "" {
			body += ": " + truncateOpenCodeText(prompt, 300)
		}
		if part.Agent != "" {
			body += " (agent: " + part.Agent + ")"
		}
		return &event.MessageEventContent{MsgType: event.MsgNotice, Body: body}, nil, nil
	case "retry":
		body := fmt.Sprintf("Retry attempt %d", part.Attempt)
		if len(part.Error) > 0 {
			body += ": " + truncateOpenCodeText(string(part.Error), 300)
		}
		return &event.MessageEventContent{MsgType: event.MsgNotice, Body: body}, nil, nil
	case "compaction":
		body := fmt.Sprintf("Compaction (auto: %t)", part.Auto)
		return &event.MessageEventContent{MsgType: event.MsgNotice, Body: body}, nil, nil
	default:
		return &event.MessageEventContent{MsgType: event.MsgNotice, Body: "OpenCode part: " + part.Type}, nil, nil
	}
}

func truncateOpenCodeText(text string, max int) string {
	if max <= 0 || len(text) <= max {
		return text
	}
	return text[:max] + "..."
}

func toolDisplayTitle(toolName string) string {
	toolName = strings.TrimSpace(toolName)
	if tool := tools.GetTool(toolName); tool != nil && tool.Annotations != nil && tool.Annotations.Title != "" {
		return tool.Annotations.Title
	}
	return toolName
}
