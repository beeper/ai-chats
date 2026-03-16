package opencode

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/bridges/opencode/api"
	"github.com/beeper/agentremote/pkg/shared/backfillutil"
)

type backfillMessageEntry struct {
	msg  api.MessageWithParts
	when time.Time
}

func (b *Bridge) FetchMessages(ctx context.Context, params bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	if b == nil || b.manager == nil || params.Portal == nil {
		return nil, nil
	}
	if params.ThreadRoot != "" {
		return nil, nil
	}
	meta := b.portalMeta(params.Portal)
	if meta == nil || !meta.IsOpenCodeRoom {
		return nil, nil
	}
	inst := b.manager.getInstance(meta.InstanceID)
	if inst == nil {
		return nil, errors.New("OpenCode instance not connected")
	}
	if strings.TrimSpace(meta.SessionID) == "" {
		return nil, errors.New("OpenCode session ID is required")
	}
	messages, err := inst.listMessagesForBackfill(ctx, meta.SessionID, params.Forward, params.Count)
	if err != nil {
		if api.IsAuthError(err) {
			b.manager.setConnected(inst, false)
		}
		return nil, err
	}
	if len(messages) == 0 {
		return &bridgev2.FetchMessagesResponse{HasMore: false, Forward: params.Forward}, nil
	}
	entries := make([]backfillMessageEntry, 0, len(messages))
	for _, msg := range messages {
		entries = append(entries, backfillMessageEntry{msg: msg, when: openCodeMessageTime(msg)})
	}
	slices.SortStableFunc(entries, func(a, b backfillMessageEntry) int {
		if c := a.when.Compare(b.when); c != 0 {
			return c
		}
		return cmp.Compare(a.msg.Info.ID, b.msg.Info.ID)
	})

	msgIndex, partIndex := buildAnchorIndexMaps(entries)
	result := backfillutil.Paginate(
		len(entries),
		backfillutil.PaginateParams{
			Count:         params.Count,
			Forward:       params.Forward,
			Cursor:        params.Cursor,
			AnchorMessage: params.AnchorMessage,
		},
		func(anchor *database.Message) (int, bool) {
			return findAnchorIndex(msgIndex, partIndex, anchor)
		},
		func(anchor *database.Message) int {
			return backfillutil.IndexAtOrAfter(len(entries), func(i int) time.Time {
				return entries[i].when
			}, anchor.Timestamp)
		},
	)
	batch := entries[result.Start:result.End]
	cursor := result.Cursor
	hasMore := result.HasMore

	if len(batch) == 0 {
		return &bridgev2.FetchMessagesResponse{HasMore: hasMore, Forward: params.Forward, Cursor: cursor}, nil
	}

	backfillMessages, err := b.convertOpenCodeBackfill(ctx, params.Portal, meta.InstanceID, batch)
	if err != nil {
		return nil, err
	}
	return &bridgev2.FetchMessagesResponse{
		Messages:                backfillMessages,
		Cursor:                  cursor,
		HasMore:                 hasMore,
		Forward:                 params.Forward,
		AggressiveDeduplication: true,
		ApproxTotalCount:        len(entries),
	}, nil
}

func buildAnchorIndexMaps(entries []backfillMessageEntry) (msgIndex, partIndex map[string]int) {
	msgIndex = make(map[string]int, len(entries))
	partIndex = make(map[string]int, len(entries))
	for i, entry := range entries {
		if entry.msg.Info.ID != "" {
			msgIndex[entry.msg.Info.ID] = i
		}
		for _, part := range entry.msg.Parts {
			if part.ID != "" {
				partIndex[part.ID] = i
			}
			if part.State != nil {
				for _, attachment := range part.State.Attachments {
					if attachment.ID != "" {
						partIndex[attachment.ID] = i
					}
				}
			}
		}
	}
	return msgIndex, partIndex
}

func findAnchorIndex(msgIndex, partIndex map[string]int, anchor *database.Message) (int, bool) {
	if anchor == nil || anchor.ID == "" {
		return 0, false
	}
	partID, isPart := parseOpenCodePartID(anchor.ID)
	msgID, isMsg := parseOpenCodeMessageID(anchor.ID)
	if !isPart && !isMsg {
		return 0, false
	}
	if isPart {
		if idx, ok := partIndex[partID]; ok {
			return idx, true
		}
	}
	if isMsg {
		if idx, ok := msgIndex[msgID]; ok {
			return idx, true
		}
	}
	return 0, false
}

func openCodeMessageTime(msg api.MessageWithParts) time.Time {
	if msg.Info.Time.Created > 0 {
		return time.UnixMilli(int64(msg.Info.Time.Created))
	}
	if msg.Info.Time.Completed > 0 {
		return time.UnixMilli(int64(msg.Info.Time.Completed))
	}
	for _, part := range msg.Parts {
		if part.Time != nil && part.Time.Start > 0 {
			return time.UnixMilli(int64(part.Time.Start))
		}
		if part.State != nil && part.State.Time != nil && part.State.Time.Start > 0 {
			return time.UnixMilli(int64(part.State.Time.Start))
		}
	}
	return time.Unix(0, 0)
}

func parseOpenCodePartID(msgID networkid.MessageID) (string, bool) {
	raw := string(msgID)
	if after, ok := strings.CutPrefix(raw, "opencode:part:"); ok {
		return after, true
	}
	return "", false
}

func parseOpenCodeMessageID(msgID networkid.MessageID) (string, bool) {
	raw := string(msgID)
	if strings.HasPrefix(raw, "opencode:part:") {
		return "", false
	}
	if value, ok := strings.CutPrefix(raw, "opencode:"); ok && value != "" {
		return value, true
	}
	return "", false
}

func (b *Bridge) convertOpenCodeBackfill(ctx context.Context, portal *bridgev2.Portal, instanceID string, batch []backfillMessageEntry) ([]*bridgev2.BackfillMessage, error) {
	if b == nil || portal == nil || b.host == nil {
		return nil, nil
	}
	login := b.host.GetUserLogin()
	if login == nil {
		return nil, nil
	}
	var lastStreamOrder int64
	var out []*bridgev2.BackfillMessage
	for _, entry := range batch {
		msg := entry.msg
		role := strings.ToLower(strings.TrimSpace(msg.Info.Role))
		fromMe := role == "user"
		sender := b.opencodeSender(instanceID, fromMe)
		intent, ok := portal.GetIntentFor(ctx, sender, login, bridgev2.RemoteEventMessage)
		if !ok || intent == nil {
			continue
		}
		msgTime := entry.when
		baseOrder := msgTime.UnixMilli() * 1000
		if baseOrder <= 0 {
			baseOrder = lastStreamOrder + 1
		}
		nextOrder := func() int64 {
			order := baseOrder
			if order <= lastStreamOrder {
				order = lastStreamOrder + 1
			}
			lastStreamOrder = order
			baseOrder = order + 1
			return order
		}
		if role == "user" {
			userBackfill, err := b.buildOpenCodeUserBackfillMessages(ctx, portal, intent, sender, msg, msgTime, nextOrder)
			if err != nil {
				return nil, err
			}
			out = append(out, userBackfill...)
			continue
		}
		snapshot := buildCanonicalAssistantBackfill(msg, b.portalAgentID(portal))
		out = append(out, &bridgev2.BackfillMessage{
			ConvertedMessage: &bridgev2.ConvertedMessage{
				Parts: []*bridgev2.ConvertedMessagePart{{
					ID:         networkid.PartID("0"),
					Type:       event.EventMessage,
					Content:    buildCanonicalBackfillPart(snapshot),
					Extra:      canonicalBackfillExtra(snapshot),
					DBMetadata: snapshot.meta,
				}},
			},
			Sender:      sender,
			ID:          networkid.MessageID("opencode:" + msg.Info.ID),
			TxnID:       networkid.TransactionID("opencode:" + msg.Info.ID),
			Timestamp:   msgTime,
			StreamOrder: nextOrder(),
		})
	}
	return out, nil
}

func (b *Bridge) buildOpenCodeUserBackfillMessages(
	ctx context.Context,
	portal *bridgev2.Portal,
	intent bridgev2.MatrixAPI,
	sender bridgev2.EventSender,
	msg api.MessageWithParts,
	msgTime time.Time,
	nextOrder func() int64,
) ([]*bridgev2.BackfillMessage, error) {
	out := make([]*bridgev2.BackfillMessage, 0, len(msg.Parts))
	for _, part := range msg.Parts {
		if part.ID == "" {
			continue
		}
		fillPartIDs(&part, msg.Info.ID, msg.Info.SessionID)
		cmp, err := b.buildOpenCodeConvertedPart(ctx, portal, intent, part)
		if err != nil {
			if errors.Is(err, bridgev2.ErrIgnoringRemoteEvent) {
				continue
			}
			return nil, err
		} else if cmp == nil {
			continue
		}
		msgID := opencodePartMessageID(part.ID)
		out = append(out, &bridgev2.BackfillMessage{
			ConvertedMessage: &bridgev2.ConvertedMessage{
				Parts: []*bridgev2.ConvertedMessagePart{cmp},
			},
			Sender:      sender,
			ID:          msgID,
			TxnID:       networkid.TransactionID(msgID),
			Timestamp:   msgTime,
			StreamOrder: nextOrder(),
		})
	}
	return out, nil
}
