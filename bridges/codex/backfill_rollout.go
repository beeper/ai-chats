package codex

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

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
