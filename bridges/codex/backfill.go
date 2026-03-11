package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/pkg/bridgeadapter"
	"github.com/beeper/agentremote/pkg/shared/backfillutil"
)

const codexThreadListPageSize = 100

var codexThreadListSourceKinds = []string{"cli", "vscode", "appServer"}

type codexThread struct {
	ID        string      `json:"id"`
	Preview   string      `json:"preview"`
	Name      string      `json:"name"`
	Cwd       string      `json:"cwd"`
	CreatedAt int64       `json:"createdAt"`
	UpdatedAt int64       `json:"updatedAt"`
	Turns     []codexTurn `json:"turns"`
}

type codexThreadListResponse struct {
	Data       []codexThread `json:"data"`
	NextCursor string        `json:"nextCursor"`
}

type codexThreadReadResponse struct {
	Thread codexThread `json:"thread"`
}

type codexTurn struct {
	ID    string          `json:"id"`
	Items []codexTurnItem `json:"items"`
}

type codexTurnItem struct {
	Type    string           `json:"type"`
	ID      string           `json:"id"`
	Text    string           `json:"text"`
	Content []codexUserInput `json:"content"`
}

type codexUserInput struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type codexBackfillEntry struct {
	MessageID   networkid.MessageID
	Sender      bridgev2.EventSender
	Text        string
	Role        string
	TurnID      string
	Timestamp   time.Time
	StreamOrder int64
}

func (cc *CodexClient) syncStoredCodexThreads(ctx context.Context) error {
	if cc == nil || cc.UserLogin == nil || cc.UserLogin.Bridge == nil {
		return nil
	}
	if err := cc.ensureRPC(ctx); err != nil {
		return err
	}
	threads, err := cc.listCodexThreads(ctx)
	if err != nil {
		return err
	}
	if len(threads) == 0 {
		return nil
	}

	portalsByThreadID, err := cc.existingCodexPortalsByThreadID(ctx)
	if err != nil {
		return err
	}

	createdCount := 0
	for _, thread := range threads {
		threadID := strings.TrimSpace(thread.ID)
		if threadID == "" {
			continue
		}
		portal, created, err := cc.ensureCodexThreadPortal(ctx, portalsByThreadID[threadID], thread)
		if err != nil {
			cc.log.Warn().Err(err).Str("thread_id", threadID).Msg("Failed to sync Codex thread portal")
			continue
		}
		portalsByThreadID[threadID] = portal
		if created {
			createdCount++
		}
	}
	if createdCount > 0 {
		cc.log.Info().Int("created_rooms", createdCount).Msg("Synced stored Codex threads into Matrix")
	}
	return nil
}

func (cc *CodexClient) existingCodexPortalsByThreadID(ctx context.Context) (map[string]*bridgev2.Portal, error) {
	if cc == nil || cc.UserLogin == nil || cc.UserLogin.Bridge == nil || cc.UserLogin.Bridge.DB == nil {
		return map[string]*bridgev2.Portal{}, nil
	}
	userPortals, err := cc.UserLogin.Bridge.DB.UserPortal.GetAllForLogin(ctx, cc.UserLogin.UserLogin)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*bridgev2.Portal, len(userPortals))
	for _, userPortal := range userPortals {
		if userPortal == nil {
			continue
		}
		portal, err := cc.UserLogin.Bridge.GetExistingPortalByKey(ctx, userPortal.Portal)
		if err != nil || portal == nil {
			continue
		}
		meta := portalMeta(portal)
		if meta == nil || !meta.IsCodexRoom {
			continue
		}
		threadID := strings.TrimSpace(meta.CodexThreadID)
		if threadID == "" {
			continue
		}
		if _, exists := out[threadID]; exists {
			continue
		}
		out[threadID] = portal
	}
	return out, nil
}

func (cc *CodexClient) ensureCodexThreadPortal(ctx context.Context, existing *bridgev2.Portal, thread codexThread) (*bridgev2.Portal, bool, error) {
	if cc == nil || cc.UserLogin == nil || cc.UserLogin.Bridge == nil {
		return nil, false, errors.New("login unavailable")
	}
	threadID := strings.TrimSpace(thread.ID)
	if threadID == "" {
		return nil, false, errors.New("missing thread id")
	}

	portal := existing
	var err error
	if portal == nil {
		portal, err = cc.UserLogin.Bridge.GetPortalByKey(ctx, codexThreadPortalKey(cc.UserLogin.ID, threadID))
		if err != nil {
			return nil, false, err
		}
	}
	created := portal.MXID == ""

	if portal.Metadata == nil {
		portal.Metadata = &PortalMetadata{}
	}
	meta := portalMeta(portal)
	meta.IsCodexRoom = true
	meta.CodexThreadID = threadID
	if cwd := strings.TrimSpace(thread.Cwd); cwd != "" {
		meta.CodexCwd = cwd
	}
	meta.AwaitingCwdSetup = strings.TrimSpace(meta.CodexCwd) == ""

	title := codexThreadTitle(thread)
	if title == "" {
		title = "Codex"
	}
	meta.Title = title
	if meta.Slug == "" {
		meta.Slug = codexThreadSlug(threadID)
	}

	portal.RoomType = database.RoomTypeDM
	portal.OtherUserID = codexGhostID
	portal.Name = title
	portal.NameSet = true

	if err := portal.Save(ctx); err != nil {
		return nil, false, err
	}

	info := cc.composeCodexChatInfo(title, true)
	if portal.MXID == "" {
		if err := portal.CreateMatrixRoom(ctx, cc.UserLogin, info); err != nil {
			return nil, false, err
		}
		bridgeadapter.SendAIRoomInfo(ctx, portal, bridgeadapter.AIRoomKindAgent)
		if meta.AwaitingCwdSetup {
			cc.sendSystemNotice(ctx, portal, "This imported conversation needs a working directory. Send an absolute path or `~/...`.")
		}
	} else {
		if err := cc.UserLogin.Bridge.DB.BackfillTask.EnsureExists(ctx, portal.PortalKey, cc.UserLogin.ID); err != nil {
			cc.log.Warn().Err(err).Str("thread_id", threadID).Msg("Failed to ensure Codex backfill task")
		} else {
			cc.UserLogin.Bridge.WakeupBackfillQueue()
		}
	}

	return portal, created, nil
}

func codexThreadTitle(thread codexThread) string {
	if title := strings.TrimSpace(thread.Name); title != "" {
		return title
	}
	preview := strings.TrimSpace(thread.Preview)
	if preview == "" {
		return "Codex"
	}
	preview = strings.ReplaceAll(preview, "\r", "")
	if line, _, ok := strings.Cut(preview, "\n"); ok {
		preview = line
	}
	const max = 120
	if len(preview) > max {
		preview = preview[:max]
	}
	return strings.TrimSpace(preview)
}

func codexThreadSlug(threadID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(threadID)))
	return "thread-" + hex.EncodeToString(sum[:6])
}

func (cc *CodexClient) listCodexThreads(ctx context.Context) ([]codexThread, error) {
	if err := cc.ensureRPC(ctx); err != nil {
		return nil, err
	}
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
	if err != nil && includeTurns && shouldRetryThreadReadWithoutTurns(err) {
		return cc.readCodexThread(ctx, threadID, false)
	}
	if err != nil {
		return nil, err
	}
	return &resp.Thread, nil
}

func shouldRetryThreadReadWithoutTurns(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "includeturns is unavailable") ||
		strings.Contains(msg, "before first user message") ||
		strings.Contains(msg, "ephemeral threads do not support includeturns")
}

func (cc *CodexClient) FetchMessages(ctx context.Context, params bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	if params.Portal == nil || params.ThreadRoot != "" {
		return nil, nil
	}
	meta := portalMeta(params.Portal)
	if meta == nil || !meta.IsCodexRoom {
		return nil, nil
	}
	threadID := strings.TrimSpace(meta.CodexThreadID)
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
	entries := codexThreadBackfillEntries(*thread, cc.senderForHuman(), cc.senderForPortal())
	if len(entries) == 0 {
		return &bridgev2.FetchMessagesResponse{
			HasMore:  false,
			Forward:  params.Forward,
			Cursor:   "",
			Messages: nil,
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
				MsgType: event.MsgText,
				Body:    text,
			},
			Extra: map[string]any{
				"msgtype":    event.MsgText,
				"body":       text,
				"m.mentions": map[string]any{},
			},
			DBMetadata: &MessageMetadata{
				BaseMessageMetadata: bridgeadapter.BaseMessageMetadata{
					Role:   role,
					Body:   text,
					TurnID: turnID,
				},
			},
		}},
	}
}

func codexThreadBackfillEntries(thread codexThread, humanSender, codexSender bridgev2.EventSender) []codexBackfillEntry {
	if len(thread.Turns) == 0 {
		return nil
	}
	baseUnix := thread.CreatedAt
	if baseUnix <= 0 {
		baseUnix = thread.UpdatedAt
	}
	if baseUnix <= 0 {
		baseUnix = time.Now().UTC().Unix()
	}
	baseTime := time.Unix(baseUnix, 0).UTC()
	nextOrder := baseTime.UnixMilli() * 1000

	var out []codexBackfillEntry
	for idx, turn := range thread.Turns {
		userText, assistantText := codexTurnTextPair(turn)
		turnID := strings.TrimSpace(turn.ID)
		if turnID == "" {
			turnID = fmt.Sprintf("turn-%d", idx)
		}
		turnTime := baseTime.Add(time.Duration(idx*2) * time.Second)
		if userText != "" {
			out = append(out, codexBackfillEntry{
				MessageID:   codexBackfillMessageID(thread.ID, turnID, "user"),
				Sender:      humanSender,
				Text:        userText,
				Role:        "user",
				TurnID:      turnID,
				Timestamp:   turnTime,
				StreamOrder: nextOrder,
			})
			nextOrder++
		}
		if assistantText != "" {
			out = append(out, codexBackfillEntry{
				MessageID:   codexBackfillMessageID(thread.ID, turnID, "assistant"),
				Sender:      codexSender,
				Text:        assistantText,
				Role:        "assistant",
				TurnID:      turnID,
				Timestamp:   turnTime.Add(time.Second),
				StreamOrder: nextOrder,
			})
			nextOrder++
		}
	}
	return out
}

func codexTurnTextPair(turn codexTurn) (string, string) {
	var userTextParts []string
	var assistantOrder []string
	assistantTextByID := make(map[string]string)
	var assistantLoose []string

	for _, item := range turn.Items {
		switch normalizeCodexThreadItemType(item.Type) {
		case "usermessage":
			for _, input := range item.Content {
				if strings.ToLower(strings.TrimSpace(input.Type)) != "text" {
					continue
				}
				text := strings.TrimSpace(input.Text)
				if text == "" {
					continue
				}
				userTextParts = append(userTextParts, text)
			}
		case "agentmessage":
			text := strings.TrimSpace(item.Text)
			if text == "" {
				continue
			}
			itemID := strings.TrimSpace(item.ID)
			if itemID == "" {
				assistantLoose = append(assistantLoose, text)
				continue
			}
			if _, exists := assistantTextByID[itemID]; !exists {
				assistantOrder = append(assistantOrder, itemID)
			}
			assistantTextByID[itemID] = text
		}
	}

	userText := strings.TrimSpace(strings.Join(userTextParts, "\n\n"))
	assistantTextParts := make([]string, 0, len(assistantOrder)+len(assistantLoose))
	for _, itemID := range assistantOrder {
		if text := strings.TrimSpace(assistantTextByID[itemID]); text != "" {
			assistantTextParts = append(assistantTextParts, text)
		}
	}
	assistantTextParts = append(assistantTextParts, assistantLoose...)
	assistantText := strings.TrimSpace(strings.Join(assistantTextParts, "\n\n"))
	return userText, assistantText
}

func normalizeCodexThreadItemType(itemType string) string {
	normalized := strings.ToLower(strings.TrimSpace(itemType))
	normalized = strings.ReplaceAll(normalized, "_", "")
	return normalized
}

func codexBackfillMessageID(threadID, turnID, role string) networkid.MessageID {
	trimmedThreadID := strings.TrimSpace(threadID)
	trimmedTurnID := strings.TrimSpace(turnID)
	trimmedRole := strings.TrimSpace(role)
	hashInput := trimmedThreadID + "\n" + trimmedTurnID + "\n" + trimmedRole
	sum := sha256.Sum256([]byte(hashInput))
	return networkid.MessageID("codex:history:" + hex.EncodeToString(sum[:12]))
}

func codexPaginateBackfill(entries []codexBackfillEntry, params bridgev2.FetchMessagesParams) ([]codexBackfillEntry, networkid.PaginationCursor, bool) {
	count := params.Count
	if count <= 0 {
		count = len(entries)
	}
	if params.Forward {
		start := 0
		if params.AnchorMessage != nil {
			if anchorIdx, ok := findCodexAnchorIndex(entries, params.AnchorMessage); ok {
				start = anchorIdx + 1
			} else {
				start = codexIndexAtOrAfter(entries, params.AnchorMessage.Timestamp)
			}
		}
		if start < 0 {
			start = 0
		}
		if start > len(entries) {
			start = len(entries)
		}
		end := len(entries)
		hasMore := false
		if start+count < end {
			end = start + count
			hasMore = true
		}
		return entries[start:end], "", hasMore
	}

	end := len(entries)
	if params.Cursor != "" {
		if idx, ok := backfillutil.ParseCursor(params.Cursor); ok && idx >= 0 && idx <= len(entries) {
			end = idx
		}
	} else if params.AnchorMessage != nil {
		if anchorIdx, ok := findCodexAnchorIndex(entries, params.AnchorMessage); ok {
			end = anchorIdx
		} else {
			end = codexIndexAtOrAfter(entries, params.AnchorMessage.Timestamp)
		}
	}
	if end < 0 {
		end = 0
	}
	if end > len(entries) {
		end = len(entries)
	}
	start := end - count
	if start < 0 {
		start = 0
	}
	hasMore := start > 0
	cursor := networkid.PaginationCursor("")
	if hasMore {
		cursor = backfillutil.FormatCursor(start)
	}
	return entries[start:end], cursor, hasMore
}

func findCodexAnchorIndex(entries []codexBackfillEntry, anchor *database.Message) (int, bool) {
	if anchor == nil || anchor.ID == "" {
		return 0, false
	}
	for idx, entry := range entries {
		if entry.MessageID == anchor.ID {
			return idx, true
		}
	}
	return 0, false
}

func codexIndexAtOrAfter(entries []codexBackfillEntry, anchor time.Time) int {
	if anchor.IsZero() {
		return 0
	}
	return sort.Search(len(entries), func(i int) bool {
		return !entries[i].Timestamp.Before(anchor)
	})
}
