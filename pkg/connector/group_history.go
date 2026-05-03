package connector

import (
	"strings"
	"time"

	"maunium.net/go/mautrix/id"
)

const (
	defaultGroupHistoryLimit = 20
	maxGroupHistoryKeys      = 1000
	groupHistoryMarker       = "[Chat messages since your last reply - for context]"
	currentMessageMarker     = "[Current message - respond to this]"
)

type groupHistoryEntry struct {
	Body string
}

type groupHistoryBuffer struct {
	Entries []groupHistoryEntry
	Touched int64
}

func (oc *AIClient) resolveGroupHistoryLimit() int {
	if oc == nil || oc.connector == nil {
		return defaultGroupHistoryLimit
	}
	if oc.connector.Config.Messages != nil && oc.connector.Config.Messages.GroupChat != nil {
		limit := oc.connector.Config.Messages.GroupChat.HistoryLimit
		if limit >= 0 {
			return limit
		}
	}
	return defaultGroupHistoryLimit
}

func (oc *AIClient) recordPendingGroupHistory(roomID id.RoomID, body string, limit int) {
	if oc == nil || roomID == "" || limit <= 0 {
		return
	}
	cleaned := strings.TrimSpace(body)
	if cleaned == "" {
		return
	}
	oc.groupHistoryMu.Lock()
	defer oc.groupHistoryMu.Unlock()
	if oc.groupHistoryBuffers == nil {
		oc.groupHistoryBuffers = make(map[id.RoomID]*groupHistoryBuffer)
	}
	buffer := oc.groupHistoryBuffers[roomID]
	if buffer == nil {
		buffer = &groupHistoryBuffer{}
		oc.groupHistoryBuffers[roomID] = buffer
	}
	buffer.Entries = append(buffer.Entries, groupHistoryEntry{Body: cleaned})
	if len(buffer.Entries) > limit {
		buffer.Entries = buffer.Entries[len(buffer.Entries)-limit:]
	}
	buffer.Touched = time.Now().UnixMilli()

	if len(oc.groupHistoryBuffers) > maxGroupHistoryKeys {
		oc.evictOldGroupHistoryLocked()
	}
}

func (oc *AIClient) takePendingGroupHistory(roomID id.RoomID, limit int) []groupHistoryEntry {
	if oc == nil || roomID == "" || limit <= 0 {
		return nil
	}
	oc.groupHistoryMu.Lock()
	defer oc.groupHistoryMu.Unlock()
	if oc.groupHistoryBuffers == nil {
		return nil
	}
	buffer := oc.groupHistoryBuffers[roomID]
	if buffer == nil || len(buffer.Entries) == 0 {
		return nil
	}
	entries := buffer.Entries
	delete(oc.groupHistoryBuffers, roomID)
	return entries
}

func (oc *AIClient) buildGroupHistoryContext(roomID id.RoomID, current string, limit int) string {
	if limit <= 0 {
		return current
	}
	entries := oc.takePendingGroupHistory(roomID, limit)
	if len(entries) == 0 {
		return current
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.Body) != "" {
			lines = append(lines, entry.Body)
		}
	}
	if len(lines) == 0 {
		return current
	}
	return strings.Join([]string{
		groupHistoryMarker,
		strings.Join(lines, "\n"),
		"",
		currentMessageMarker,
		current,
	}, "\n")
}

func (oc *AIClient) evictOldGroupHistoryLocked() {
	if oc == nil || oc.groupHistoryBuffers == nil {
		return
	}
	if len(oc.groupHistoryBuffers) <= maxGroupHistoryKeys {
		return
	}
	var oldestRoom id.RoomID
	var oldestTS int64
	first := true
	for roomID, buffer := range oc.groupHistoryBuffers {
		if buffer == nil {
			continue
		}
		if first || buffer.Touched < oldestTS {
			oldestTS = buffer.Touched
			oldestRoom = roomID
			first = false
		}
	}
	if oldestRoom != "" {
		delete(oc.groupHistoryBuffers, oldestRoom)
	}
}
