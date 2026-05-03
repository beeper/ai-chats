package codex

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote/pkg/shared/backfillutil"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

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
	return networkid.MessageID("codex:history:" + stringutil.ShortHash(hashInput, 12))
}

func codexThreadPortalKey(loginID networkid.UserLoginID, threadID string) (networkid.PortalKey, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return networkid.PortalKey{}, fmt.Errorf("empty threadID")
	}
	return networkid.PortalKey{
		ID: networkid.PortalID(
			fmt.Sprintf(
				"codex:%s:thread:%s",
				loginID,
				url.PathEscape(threadID),
			),
		),
		Receiver: loginID,
	}, nil
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
