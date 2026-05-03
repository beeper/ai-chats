package codex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/sdk"
)

func (cc *CodexClient) listCodexThreads(ctx context.Context, cwd string) ([]codexThread, error) {
	if err := cc.ensureRPC(ctx); err != nil {
		return nil, err
	}
	cwd = strings.TrimSpace(cwd)
	var (
		cursor string
		out    []codexThread
		seen   = make(map[string]struct{})
	)
	for page := 0; page < 1000; page++ {
		params := map[string]any{
			"limit":       codexThreadListPageSize,
			"sourceKinds": codexThreadListSourceKinds,
		}
		if cwd != "" {
			params["cwd"] = cwd
		}
		if cursor != "" {
			params["cursor"] = cursor
		}

		var resp codexThreadListResponse
		callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		err := cc.rpc.Call(callCtx, "thread/list", params, &resp)
		cancel()
		if err != nil {
			return nil, err
		}
		for _, thread := range resp.Data {
			threadID := strings.TrimSpace(thread.ID)
			if threadID == "" {
				continue
			}
			if _, exists := seen[threadID]; exists {
				continue
			}
			seen[threadID] = struct{}{}
			out = append(out, thread)
		}
		next := strings.TrimSpace(resp.NextCursor)
		if next == "" || next == cursor {
			break
		}
		cursor = next
	}
	return out, nil
}

func (cc *CodexClient) readCodexThread(ctx context.Context, threadID string, includeTurns bool) (*codexThread, error) {
	if err := cc.ensureRPC(ctx); err != nil {
		return nil, err
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, errors.New("missing thread id")
	}
	var resp codexThreadReadResponse
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	err := cc.rpc.Call(callCtx, "thread/read", map[string]any{
		"threadId":     threadID,
		"includeTurns": includeTurns,
	}, &resp)
	cancel()
	if err != nil {
		return nil, err
	}
	return &resp.Thread, nil
}

func (cc *CodexClient) FetchMessages(ctx context.Context, params bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	if params.Portal == nil || params.ThreadRoot != "" {
		return nil, nil
	}
	if meta := portalMeta(params.Portal); meta == nil || !meta.IsCodexRoom {
		return nil, nil
	}
	state, err := loadCodexPortalState(ctx, params.Portal)
	if err != nil {
		return nil, err
	}
	threadID := strings.TrimSpace(state.CodexThreadID)
	if threadID == "" {
		return nil, nil
	}

	thread, err := cc.readCodexThread(ctx, threadID, true)
	if err != nil {
		return nil, fmt.Errorf("failed to read thread %s: %w", threadID, err)
	}
	if thread == nil {
		return nil, nil
	}
	timings, err := cc.loadCodexTurnTimings(*thread)
	if err != nil {
		cc.log.Warn().Err(err).Str("thread_id", threadID).Msg("Failed to load Codex rollout timings, falling back to synthetic timestamps")
	}
	entries := codexThreadBackfillEntriesWithTimings(*thread, timings, cc.senderForHuman(), cc.senderForPortal())
	if len(entries) == 0 {
		return &bridgev2.FetchMessagesResponse{
			Forward: params.Forward,
		}, nil
	}

	batch, cursor, hasMore := codexPaginateBackfill(entries, params)
	backfill := make([]*bridgev2.BackfillMessage, 0, len(batch))
	for _, entry := range batch {
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		backfill = append(backfill, &bridgev2.BackfillMessage{
			ConvertedMessage: codexBackfillConvertedMessage(entry.Role, text, entry.TurnID),
			Sender:           entry.Sender,
			ID:               entry.MessageID,
			TxnID:            networkid.TransactionID(entry.MessageID),
			Timestamp:        entry.Timestamp,
			StreamOrder:      entry.StreamOrder,
		})
	}

	return &bridgev2.FetchMessagesResponse{
		Messages:                backfill,
		Cursor:                  cursor,
		HasMore:                 hasMore,
		Forward:                 params.Forward,
		AggressiveDeduplication: true,
		ApproxTotalCount:        len(entries),
	}, nil
}

func codexBackfillConvertedMessage(role, text, turnID string) *bridgev2.ConvertedMessage {
	return &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:   networkid.PartID("0"),
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				MsgType:  event.MsgText,
				Body:     text,
				Mentions: &event.Mentions{},
			},
			DBMetadata: &MessageMetadata{
				BaseMessageMetadata: sdk.BaseMessageMetadata{
					Role:   role,
					Body:   text,
					TurnID: turnID,
				},
			},
		}},
	}
}
