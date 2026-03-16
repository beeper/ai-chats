package codex

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/pkg/shared/backfillutil"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

const codexThreadListPageSize = 100

var codexThreadListSourceKinds = []string{"cli", "vscode", "appServer"}

type codexThread struct {
	ID        string      `json:"id"`
	Preview   string      `json:"preview"`
	Name      string      `json:"name"`
	Path      string      `json:"path"`
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
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Items  []codexTurnItem `json:"items"`
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

type codexTurnTiming struct {
	TurnID             string
	UserTimestamp      time.Time
	AssistantTimestamp time.Time
	explicit           bool
}

type codexRolloutLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexRolloutEvent struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type codexRolloutTurnEvent struct {
	TurnID string `json:"turn_id"`
}

func (cc *CodexClient) syncStoredCodexThreads(ctx context.Context) error {
	if cc == nil || cc.UserLogin == nil || cc.UserLogin.Bridge == nil {
		return nil
	}
	if err := cc.ensureRPC(ctx); err != nil {
		return err
	}
	directories := managedCodexPaths(loginMetadata(cc.UserLogin))
	if len(directories) == 0 {
		return nil
	}
	totalCreated := 0
	for _, directory := range directories {
		_, createdCount, err := cc.syncStoredCodexThreadsForPath(ctx, directory)
		if err != nil {
			return err
		}
		totalCreated += createdCount
	}
	if totalCreated > 0 {
		cc.log.Info().Int("created_rooms", totalCreated).Msg("Synced stored Codex threads into Matrix")
	}
	return nil
}

func (cc *CodexClient) syncStoredCodexThreadsForPath(ctx context.Context, cwd string) (int, int, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return 0, 0, nil
	}
	threads, err := cc.listCodexThreads(ctx, cwd)
	if err != nil {
		return 0, 0, err
	}
	if len(threads) == 0 {
		return 0, 0, nil
	}
	portalsByThreadID, err := cc.existingCodexPortalsByThreadID(ctx)
	if err != nil {
		return 0, 0, err
	}
	createdCount := 0
	for _, thread := range threads {
		threadID := strings.TrimSpace(thread.ID)
		if threadID == "" {
			continue
		}
		portal, created, err := cc.ensureCodexThreadPortal(ctx, portalsByThreadID[threadID], thread)
		if err != nil {
			cc.log.Warn().Err(err).Str("thread_id", threadID).Str("cwd", cwd).Msg("Failed to sync Codex thread portal")
			continue
		}
		portalsByThreadID[threadID] = portal
		if created {
			createdCount++
		}
	}
	return len(threads), createdCount, nil
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
		portalKey, keyErr := codexThreadPortalKey(cc.UserLogin.ID, threadID)
		if keyErr != nil {
			return nil, false, keyErr
		}
		portal, err = cc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
		if err != nil {
			return nil, false, err
		}
	}
	var created bool
	if portal.Metadata == nil {
		portal.Metadata = &PortalMetadata{}
	}
	meta := portalMeta(portal)
	meta.IsCodexRoom = true
	meta.CodexThreadID = threadID
	meta.ManagedImport = true
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

	info := cc.composeCodexChatInfo(portal, title, true)
	portal.Name = title
	portal.NameSet = true
	created, err = bridgesdk.EnsurePortalLifecycle(ctx, bridgesdk.PortalLifecycleOptions{
		Login:             cc.UserLogin,
		Portal:            portal,
		ChatInfo:          info,
		SaveBeforeCreate:  true,
		AIRoomKind:        agentremote.AIRoomKindAgent,
		ForceCapabilities: true,
	})
	if err != nil {
		return nil, false, err
	}
	if created {
		if meta.AwaitingCwdSetup {
			cc.sendSystemNotice(ctx, portal, "This imported conversation needs a working directory. Send an absolute path or `~/...`.")
		}
	} else {
		cc.UserLogin.Bridge.WakeupBackfillQueue()
	}
	if err := portal.Save(ctx); err != nil {
		return nil, false, err
	}
	cc.syncCodexRoomTopic(ctx, portal, meta)

	return portal, created, nil
}

func codexThreadTitle(thread codexThread) string {
	if title := strings.TrimSpace(thread.Name); title != "" {
		return title
	}
	preview := strings.TrimSpace(thread.Preview)
	if preview == "" {
		return ""
	}
	// Use only the first line, truncated to 120 characters.
	line, _, _ := strings.Cut(strings.ReplaceAll(preview, "\r", ""), "\n")
	const maxLen = 120
	if len(line) > maxLen {
		line = line[:maxLen]
	}
	return strings.TrimSpace(line)
}

func codexThreadSlug(threadID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(threadID)))
	return "thread-" + hex.EncodeToString(sum[:6])
}

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
				MsgType: event.MsgText,
				Body:    text,
			},
			Extra: map[string]any{
				"msgtype":    event.MsgText,
				"body":       text,
				"m.mentions": map[string]any{},
			},
			DBMetadata: &MessageMetadata{
				BaseMessageMetadata: agentremote.BaseMessageMetadata{
					Role:   role,
					Body:   text,
					TurnID: turnID,
				},
			},
		}},
	}
}

func codexThreadBackfillEntriesWithTimings(thread codexThread, timings []codexTurnTiming, humanSender, codexSender bridgev2.EventSender) []codexBackfillEntry {
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
	resolvedTimings := codexResolveTurnTimings(thread.Turns, timings)

	var out []codexBackfillEntry
	var lastStreamOrder int64
	for idx, turn := range thread.Turns {
		userText, assistantText := codexTurnTextPair(turn)
		turnID := strings.TrimSpace(turn.ID)
		if turnID == "" {
			turnID = fmt.Sprintf("turn-%d", idx)
		}
		syntheticUserTS := baseTime.Add(time.Duration(idx*2) * time.Second)
		syntheticAssistantTS := syntheticUserTS.Add(time.Millisecond)
		turnTiming := resolvedTimings[idx]
		userTS := turnTiming.UserTimestamp
		assistantTS := turnTiming.AssistantTimestamp
		if userText != "" && userTS.IsZero() {
			if !assistantTS.IsZero() {
				userTS = assistantTS.Add(-time.Millisecond)
			} else {
				userTS = syntheticUserTS
			}
		}
		if assistantText != "" && assistantTS.IsZero() {
			if !userTS.IsZero() {
				assistantTS = userTS.Add(time.Millisecond)
			} else {
				assistantTS = syntheticAssistantTS
			}
		}
		if !userTS.IsZero() && !assistantTS.IsZero() && !assistantTS.After(userTS) {
			assistantTS = userTS.Add(time.Millisecond)
		}
		if userText != "" {
			lastStreamOrder = backfillutil.NextStreamOrder(lastStreamOrder, userTS)
			out = append(out, codexBackfillEntry{
				MessageID:   codexBackfillMessageID(thread.ID, turnID, "user"),
				Sender:      humanSender,
				Text:        userText,
				Role:        "user",
				TurnID:      turnID,
				Timestamp:   userTS,
				StreamOrder: lastStreamOrder,
			})
		}
		if assistantText != "" {
			lastStreamOrder = backfillutil.NextStreamOrder(lastStreamOrder, assistantTS)
			out = append(out, codexBackfillEntry{
				MessageID:   codexBackfillMessageID(thread.ID, turnID, "assistant"),
				Sender:      codexSender,
				Text:        assistantText,
				Role:        "assistant",
				TurnID:      turnID,
				Timestamp:   assistantTS,
				StreamOrder: lastStreamOrder,
			})
		}
	}
	return out
}

func (cc *CodexClient) loadCodexTurnTimings(thread codexThread) ([]codexTurnTiming, error) {
	rolloutPath := strings.TrimSpace(thread.Path)
	if rolloutPath == "" {
		rolloutPath = resolveCodexRolloutPath(strings.TrimSpace(loginMetadata(cc.UserLogin).CodexHome), strings.TrimSpace(thread.ID))
	}
	if rolloutPath == "" {
		return nil, nil
	}
	return readCodexTurnTimingsFromRollout(rolloutPath)
}

func resolveCodexRolloutPath(codexHome, threadID string) string {
	codexHome = strings.TrimSpace(codexHome)
	threadID = strings.TrimSpace(threadID)
	if codexHome == "" || threadID == "" {
		return ""
	}
	for _, subdir := range []string{"sessions", "archived_sessions"} {
		pattern := filepath.Join(codexHome, subdir, "*", "*", "*", "rollout-*-"+threadID+".jsonl")
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			continue
		}
		slices.Sort(matches)
		return matches[len(matches)-1]
	}
	return ""
}

func readCodexTurnTimingsFromRollout(path string) ([]codexTurnTiming, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var timings []codexTurnTiming
	var current *codexTurnTiming
	finishCurrent := func() {
		if current == nil {
			return
		}
		if current.UserTimestamp.IsZero() && current.AssistantTimestamp.IsZero() {
			current = nil
			return
		}
		timings = append(timings, *current)
		current = nil
	}
	startImplicit := func() {
		current = &codexTurnTiming{}
	}
	startExplicit := func(turnID string) {
		finishCurrent()
		current = &codexTurnTiming{TurnID: strings.TrimSpace(turnID), explicit: true}
	}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rolloutLine codexRolloutLine
		if err := json.Unmarshal([]byte(line), &rolloutLine); err != nil {
			continue
		}
		if rolloutLine.Type != "event_msg" {
			continue
		}
		ts, ok := parseCodexRolloutTimestamp(rolloutLine.Timestamp)
		if !ok {
			continue
		}
		var event codexRolloutEvent
		if err := json.Unmarshal(rolloutLine.Payload, &event); err != nil {
			continue
		}
		switch event.Type {
		case "turn_started":
			var payload codexRolloutTurnEvent
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				continue
			}
			startExplicit(payload.TurnID)
		case "turn_complete":
			var payload codexRolloutTurnEvent
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				continue
			}
			if current != nil && strings.TrimSpace(current.TurnID) == strings.TrimSpace(payload.TurnID) {
				finishCurrent()
			}
		case "user_message":
			if current == nil {
				startImplicit()
			} else if !current.explicit && (!current.UserTimestamp.IsZero() || !current.AssistantTimestamp.IsZero()) {
				finishCurrent()
				startImplicit()
			}
			if current.UserTimestamp.IsZero() {
				current.UserTimestamp = ts
			}
		case "agent_message":
			if current == nil {
				startImplicit()
			}
			current.AssistantTimestamp = ts
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	finishCurrent()
	return timings, nil
}

func parseCodexRolloutTimestamp(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15-04-05.999999999",
		"2006-01-02T15-04-05",
	} {
		ts, err := time.Parse(layout, value)
		if err == nil {
			return ts.UTC(), true
		}
	}
	return time.Time{}, false
}

func codexResolveTurnTimings(turns []codexTurn, timings []codexTurnTiming) []codexTurnTiming {
	resolved := make([]codexTurnTiming, len(turns))
	if len(turns) == 0 || len(timings) == 0 {
		return resolved
	}
	used := make([]bool, len(timings))
	for i, turn := range turns {
		turnID := strings.TrimSpace(turn.ID)
		if turnID == "" {
			continue
		}
		for j, timing := range timings {
			if used[j] || strings.TrimSpace(timing.TurnID) != turnID {
				continue
			}
			resolved[i] = timing
			used[j] = true
			break
		}
	}
	nextTiming := 0
	for i := range turns {
		if !resolved[i].UserTimestamp.IsZero() || !resolved[i].AssistantTimestamp.IsZero() {
			continue
		}
		for nextTiming < len(timings) && used[nextTiming] {
			nextTiming++
		}
		if nextTiming >= len(timings) {
			break
		}
		resolved[i] = timings[nextTiming]
		used[nextTiming] = true
		nextTiming++
	}
	return resolved
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
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(itemType)), "_", "")
}

func codexBackfillMessageID(threadID, turnID, role string) networkid.MessageID {
	hashInput := strings.TrimSpace(threadID) + "\n" + strings.TrimSpace(turnID) + "\n" + strings.TrimSpace(role)
	sum := sha256.Sum256([]byte(hashInput))
	return networkid.MessageID("codex:history:" + hex.EncodeToString(sum[:12]))
}

func codexPaginateBackfill(entries []codexBackfillEntry, params bridgev2.FetchMessagesParams) ([]codexBackfillEntry, networkid.PaginationCursor, bool) {
	result := backfillutil.Paginate(
		len(entries),
		backfillutil.PaginateParams{
			Count:              params.Count,
			Forward:            params.Forward,
			Cursor:             params.Cursor,
			AnchorMessage:      params.AnchorMessage,
			ForwardAnchorShift: 1,
		},
		func(anchor *database.Message) (int, bool) {
			return findCodexAnchorIndex(entries, anchor)
		},
		func(anchor *database.Message) int {
			return backfillutil.IndexAtOrAfter(len(entries), func(i int) time.Time {
				return entries[i].Timestamp
			}, anchor.Timestamp)
		},
	)
	return entries[result.Start:result.End], result.Cursor, result.HasMore
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
