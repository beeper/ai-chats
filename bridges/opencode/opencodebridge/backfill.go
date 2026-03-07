package opencodebridge

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/bridges/opencode/opencode"
)

type backfillMessageEntry struct {
	msg  opencode.MessageWithParts
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
		if opencode.IsAuthError(err) {
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

	var batch []backfillMessageEntry
	var cursor networkid.PaginationCursor
	var hasMore bool

	if params.Forward {
		start := 0
		if params.AnchorMessage != nil {
			if anchorIdx, ok := findAnchorIndex(entries, params.AnchorMessage); ok {
				start = anchorIdx
			} else {
				start = indexAtOrAfter(entries, params.AnchorMessage.Timestamp)
			}
		}
		end := len(entries)
		if params.Count > 0 && start+params.Count < end {
			end = start + params.Count
			hasMore = true
		}
		if start < end {
			batch = entries[start:end]
		}
	} else {
		end := len(entries)
		if params.Cursor != "" {
			if idx, ok := parseBackfillCursor(params.Cursor); ok {
				if idx >= 0 && idx <= len(entries) {
					end = idx
				}
			}
		} else if params.AnchorMessage != nil {
			if anchorIdx, ok := findAnchorIndex(entries, params.AnchorMessage); ok {
				end = anchorIdx
			} else {
				end = indexAtOrAfter(entries, params.AnchorMessage.Timestamp)
			}
		}
		if end < 0 {
			end = 0
		}
		start := end
		if params.Count > 0 {
			start = end - params.Count
		}
		if start < 0 {
			start = 0
		}
		if start < end {
			batch = entries[start:end]
		}
		hasMore = start > 0
		if hasMore {
			cursor = formatBackfillCursor(start)
		}
	}

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

func indexAtOrAfter(entries []backfillMessageEntry, anchor time.Time) int {
	if anchor.IsZero() {
		return 0
	}
	return sort.Search(len(entries), func(i int) bool {
		return !entries[i].when.Before(anchor)
	})
}

func findAnchorIndex(entries []backfillMessageEntry, anchor *database.Message) (int, bool) {
	if anchor == nil {
		return 0, false
	}
	if anchor.ID == "" {
		return 0, false
	}
	partID, isPart := parseOpenCodePartID(anchor.ID)
	msgID, isMsg := parseOpenCodeMessageID(anchor.ID)
	if !isPart && !isMsg {
		return 0, false
	}
	msgIndex := make(map[string]int, len(entries))
	partIndex := make(map[string]int, len(entries))
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

func parseBackfillCursor(cursor networkid.PaginationCursor) (int, bool) {
	if cursor == "" {
		return 0, false
	}
	idx, err := strconv.Atoi(string(cursor))
	if err != nil {
		return 0, false
	}
	return idx, true
}

func formatBackfillCursor(idx int) networkid.PaginationCursor {
	return networkid.PaginationCursor(strconv.Itoa(idx))
}

func openCodeMessageTime(msg opencode.MessageWithParts) time.Time {
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
	login := b.host.Login()
	if login == nil {
		return nil, nil
	}
	var lastStreamOrder int64
	var out []*bridgev2.BackfillMessage
	for _, entry := range batch {
		msg := entry.msg
		role := strings.ToLower(strings.TrimSpace(msg.Info.Role))
		if role == "user" {
			continue
		}
		fromMe := false
		sender := b.opencodeSender(instanceID, fromMe)
		if intent, ok := portal.GetIntentFor(ctx, sender, login, bridgev2.RemoteEventMessage); !ok || intent == nil {
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
