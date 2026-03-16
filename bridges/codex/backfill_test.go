package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
)

func TestCodexTurnTextPair(t *testing.T) {
	turn := codexTurn{
		ID: "turn_1",
		Items: []codexTurnItem{
			{
				Type: "userMessage",
				Content: []codexUserInput{
					{Type: "text", Text: "first line"},
					{Type: "mention", Text: "ignored"},
					{Type: "text", Text: "second line"},
				},
			},
			{Type: "agentMessage", ID: "a1", Text: "draft"},
			{Type: "agentMessage", ID: "a1", Text: "final"},
			{Type: "agentMessage", ID: "a2", Text: "follow-up"},
		},
	}

	userText, assistantText := codexTurnTextPair(turn)
	if userText != "first line\n\nsecond line" {
		t.Fatalf("unexpected user text: %q", userText)
	}
	if assistantText != "final\n\nfollow-up" {
		t.Fatalf("unexpected assistant text: %q", assistantText)
	}
}

func TestCodexPaginateBackfillBackward(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	entries := []codexBackfillEntry{
		{MessageID: "m1", Timestamp: now, StreamOrder: 1},
		{MessageID: "m2", Timestamp: now.Add(time.Second), StreamOrder: 2},
		{MessageID: "m3", Timestamp: now.Add(2 * time.Second), StreamOrder: 3},
	}

	firstBatch, cursor, hasMore := codexPaginateBackfill(entries, bridgev2.FetchMessagesParams{
		Forward: false,
		Count:   2,
	})
	if len(firstBatch) != 2 || string(firstBatch[0].MessageID) != "m2" || string(firstBatch[1].MessageID) != "m3" {
		t.Fatalf("unexpected first backward batch: %+v", firstBatch)
	}
	if !hasMore || cursor == "" {
		t.Fatalf("expected hasMore=true and non-empty cursor, got hasMore=%v cursor=%q", hasMore, cursor)
	}

	secondBatch, _, hasMore := codexPaginateBackfill(entries, bridgev2.FetchMessagesParams{
		Forward: false,
		Cursor:  cursor,
		Count:   2,
	})
	if len(secondBatch) != 1 || string(secondBatch[0].MessageID) != "m1" {
		t.Fatalf("unexpected second backward batch: %+v", secondBatch)
	}
	if hasMore {
		t.Fatalf("expected hasMore=false on final batch")
	}
}

func TestReadCodexTurnTimingsFromRollout(t *testing.T) {
	path := writeCodexRolloutTestFile(t, []map[string]any{
		codexRolloutTestEvent("2026-03-12T10:00:00Z", "turn_started", map[string]any{"turn_id": "turn_1"}),
		codexRolloutTestEvent("2026-03-12T10:00:01Z", "user_message", map[string]any{"message": "hello"}),
		codexRolloutTestEvent("2026-03-12T10:00:02Z", "agent_message", map[string]any{"message": "hi"}),
		codexRolloutTestEvent("2026-03-12T10:00:03Z", "turn_complete", map[string]any{"turn_id": "turn_1"}),
		codexRolloutTestEvent("2026-03-12T10:01:00Z", "turn_started", map[string]any{"turn_id": "turn_2"}),
		codexRolloutTestEvent("2026-03-12T10:01:01Z", "user_message", map[string]any{"message": "follow up"}),
		codexRolloutTestEvent("2026-03-12T10:01:05Z", "agent_message", map[string]any{"message": "done"}),
	})

	timings, err := readCodexTurnTimingsFromRollout(path)
	if err != nil {
		t.Fatalf("readCodexTurnTimingsFromRollout returned error: %v", err)
	}
	if len(timings) != 2 {
		t.Fatalf("expected 2 timings, got %d", len(timings))
	}
	if timings[0].TurnID != "turn_1" || timings[1].TurnID != "turn_2" {
		t.Fatalf("unexpected timing turn ids: %#v", timings)
	}
	if got := timings[0].UserTimestamp.UTC().Format(time.RFC3339); got != "2026-03-12T10:00:01Z" {
		t.Fatalf("unexpected first user timestamp: %s", got)
	}
	if got := timings[1].AssistantTimestamp.UTC().Format(time.RFC3339); got != "2026-03-12T10:01:05Z" {
		t.Fatalf("unexpected second assistant timestamp: %s", got)
	}
}

func TestCodexThreadBackfillEntriesWithTimingsUsesRolloutTimestamps(t *testing.T) {
	path := writeCodexRolloutTestFile(t, []map[string]any{
		codexRolloutTestEvent("2026-03-12T10:00:00Z", "turn_started", map[string]any{"turn_id": "turn_1"}),
		codexRolloutTestEvent("2026-03-12T10:00:01Z", "user_message", map[string]any{"message": "hello"}),
		codexRolloutTestEvent("2026-03-12T10:00:02Z", "agent_message", map[string]any{"message": "hi"}),
		codexRolloutTestEvent("2026-03-12T10:00:03Z", "turn_complete", map[string]any{"turn_id": "turn_1"}),
	})
	timings, err := readCodexTurnTimingsFromRollout(path)
	if err != nil {
		t.Fatalf("readCodexTurnTimingsFromRollout returned error: %v", err)
	}
	thread := codexThread{
		ID:        "thr_rollout",
		Path:      path,
		CreatedAt: 1_700_000_000,
		UpdatedAt: 1_700_000_100,
		Turns: []codexTurn{{
			ID: "turn_1",
			Items: []codexTurnItem{
				{Type: "userMessage", Content: []codexUserInput{{Type: "text", Text: "hello"}}},
				{Type: "agentMessage", ID: "a1", Text: "hi"},
			},
		}},
	}

	entries := codexThreadBackfillEntriesWithTimings(thread, timings, bridgev2.EventSender{IsFromMe: true}, bridgev2.EventSender{})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if got := entries[0].Timestamp.UTC().Format(time.RFC3339); got != "2026-03-12T10:00:01Z" {
		t.Fatalf("expected rollout user timestamp, got %s", got)
	}
	if got := entries[1].Timestamp.UTC().Format(time.RFC3339); got != "2026-03-12T10:00:02Z" {
		t.Fatalf("expected rollout assistant timestamp, got %s", got)
	}
	if !entries[1].Timestamp.After(entries[0].Timestamp) {
		t.Fatalf("expected assistant timestamp after user timestamp")
	}
	if entries[1].StreamOrder <= entries[0].StreamOrder {
		t.Fatalf("expected strictly increasing stream order, got %d then %d", entries[0].StreamOrder, entries[1].StreamOrder)
	}
}

func TestCodexThreadBackfillEntriesWithTimingsFallsBackToSyntheticTimestamps(t *testing.T) {
	thread := codexThread{
		ID:        "thr_fallback",
		CreatedAt: 1_700_000_000,
		Turns: []codexTurn{{
			ID: "turn_1",
			Items: []codexTurnItem{
				{Type: "userMessage", Content: []codexUserInput{{Type: "text", Text: "hello"}}},
				{Type: "agentMessage", ID: "a1", Text: "hi"},
			},
		}},
	}

	entries := codexThreadBackfillEntriesWithTimings(thread, nil, bridgev2.EventSender{IsFromMe: true}, bridgev2.EventSender{})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	baseTime := time.Unix(thread.CreatedAt, 0).UTC()
	if !entries[0].Timestamp.Equal(baseTime) {
		t.Fatalf("expected synthetic user timestamp %s, got %s", baseTime, entries[0].Timestamp)
	}
	if !entries[1].Timestamp.Equal(baseTime.Add(time.Millisecond)) {
		t.Fatalf("expected synthetic assistant timestamp %s, got %s", baseTime.Add(time.Millisecond), entries[1].Timestamp)
	}
}

func writeCodexRolloutTestFile(t *testing.T, lines []map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-test.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("os.Create returned error: %v", err)
	}
	defer file.Close()
	for _, line := range lines {
		data, err := json.Marshal(line)
		if err != nil {
			t.Fatalf("json.Marshal returned error: %v", err)
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			t.Fatalf("file.Write returned error: %v", err)
		}
	}
	return path
}

func codexRolloutTestEvent(ts, eventType string, payload map[string]any) map[string]any {
	return map[string]any{
		"timestamp": ts,
		"type":      "event_msg",
		"payload": map[string]any{
			"type":    eventType,
			"payload": payload,
		},
	}
}
