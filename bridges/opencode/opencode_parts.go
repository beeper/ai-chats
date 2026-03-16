package opencode

import (
	"context"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/bridges/opencode/api"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/turns"
)

type openCodePartEvent struct {
	InstanceID string
	Part       api.Part
}

func (b *Bridge) emitOpenCodePartEvent(portal *bridgev2.Portal, instanceID string, part api.Part, fromMe bool, eventType bridgev2.RemoteEventType) {
	if portal == nil || part.ID == "" {
		return
	}
	timestamp := openCodePartTimestamp(part)
	remote := &simplevent.Message[openCodePartEvent]{
		EventMeta: simplevent.EventMeta{
			Type:        eventType,
			PortalKey:   portal.PortalKey,
			Sender:      b.opencodeSender(instanceID, fromMe),
			Timestamp:   timestamp,
			StreamOrder: b.nextLiveStreamOrder(instanceID, part.SessionID, timestamp),
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

func openCodePartTimestamp(part api.Part) time.Time {
	if part.Time != nil && part.Time.Start > 0 {
		return time.UnixMilli(int64(part.Time.Start))
	}
	if part.State != nil && part.State.Time != nil {
		if part.State.Time.Start > 0 {
			return time.UnixMilli(int64(part.State.Time.Start))
		}
		if part.State.Time.Compacted > 0 {
			return time.UnixMilli(int64(part.State.Time.Compacted))
		}
		if part.State.Time.End > 0 {
			return time.UnixMilli(int64(part.State.Time.End))
		}
	}
	return time.Now()
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
	turns.EnsureDontRenderEdited(edit)
	return edit, nil
}

func (b *Bridge) buildOpenCodeConvertedPart(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, part api.Part) (*bridgev2.ConvertedMessagePart, error) {
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

func (b *Bridge) buildOpenCodePartContent(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, part api.Part) (*event.MessageEventContent, map[string]any, error) {
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
			body = "Snapshot:\n" + stringutil.Truncate(body, 4000)
		}
		return &event.MessageEventContent{MsgType: event.MsgNotice, Body: body}, nil, nil
	case "step-start":
		body := "Step started"
		if strings.TrimSpace(part.Snapshot) != "" {
			body += ": " + stringutil.Truncate(strings.TrimSpace(part.Snapshot), 200)
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
			body += ": " + stringutil.Truncate(prompt, 300)
		}
		if part.Agent != "" {
			body += " (agent: " + part.Agent + ")"
		}
		return &event.MessageEventContent{MsgType: event.MsgNotice, Body: body}, nil, nil
	case "retry":
		body := fmt.Sprintf("Retry attempt %d", part.Attempt)
		if len(part.Error) > 0 {
			body += ": " + stringutil.Truncate(string(part.Error), 300)
		}
		return &event.MessageEventContent{MsgType: event.MsgNotice, Body: body}, nil, nil
	case "compaction":
		body := fmt.Sprintf("Compaction (auto: %t)", part.Auto)
		return &event.MessageEventContent{MsgType: event.MsgNotice, Body: body}, nil, nil
	default:
		return &event.MessageEventContent{MsgType: event.MsgNotice, Body: "OpenCode part: " + part.Type}, nil, nil
	}
}
