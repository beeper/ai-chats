package ai

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"maunium.net/go/mautrix/id"
)

// ReactionFeedback represents a user reaction to an AI message.
// Similar to AgentRemote's system events, these are queued and drained
// when building the next prompt.
type ReactionFeedback struct {
	Emoji     string    // The emoji used (e.g., "👍", "👎")
	Timestamp time.Time // When the reaction was added
	Sender    string    // Who sent the reaction (display name or user ID)
	MessageID string    // Which message was reacted to (event ID or timestamp)
	RoomName  string    // Room/channel name for context
	Action    string    // "added" or "removed"
}

// ReactionQueue holds reaction feedback for a room.
type ReactionQueue struct {
	mu       sync.Mutex
	feedback []ReactionFeedback
	maxSize  int
	lastText string // For deduplication like AgentRemote
}

// reactionQueues stores per-room reaction feedback queues.
var (
	reactionQueues   = make(map[id.RoomID]*ReactionQueue)
	reactionQueuesMu sync.Mutex
)

const maxReactionFeedback = 10 // Keep last N reactions per room

func (q *ReactionQueue) addReactionLocked(feedback ReactionFeedback) {
	// Build text key for deduplication
	textKey := fmt.Sprintf("%s:%s:%s:%s", feedback.Action, feedback.Emoji, feedback.Sender, feedback.MessageID)
	if q.lastText == textKey {
		return // Skip consecutive duplicate
	}
	q.lastText = textKey

	q.feedback = append(q.feedback, feedback)
	if len(q.feedback) > q.maxSize {
		q.feedback = q.feedback[1:] // Remove oldest
	}
}

// DrainFeedback returns all queued feedback and clears the queue.
func (q *ReactionQueue) DrainFeedback() []ReactionFeedback {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.feedback) == 0 {
		return nil
	}

	result := slices.Clone(q.feedback)
	q.feedback = q.feedback[:0]
	q.lastText = "" // Reset deduplication state
	return result
}

// EnqueueReactionFeedback adds reaction feedback for a room.
func EnqueueReactionFeedback(roomID id.RoomID, feedback ReactionFeedback) {
	reactionQueuesMu.Lock()
	q, ok := reactionQueues[roomID]
	if !ok {
		q = &ReactionQueue{
			feedback: make([]ReactionFeedback, 0, maxReactionFeedback),
			maxSize:  maxReactionFeedback,
		}
		reactionQueues[roomID] = q
	}
	q.mu.Lock()
	q.addReactionLocked(feedback)
	q.mu.Unlock()
	reactionQueuesMu.Unlock()
}

// DrainReactionFeedback returns and clears all reaction feedback for a room.
// The map entry is removed once drained so that idle rooms do not leak memory.
func DrainReactionFeedback(roomID id.RoomID) []ReactionFeedback {
	reactionQueuesMu.Lock()
	q, ok := reactionQueues[roomID]
	if !ok {
		reactionQueuesMu.Unlock()
		return nil
	}
	reactionQueuesMu.Unlock()

	items := q.DrainFeedback()

	// Remove map entry when empty to avoid unbounded growth.
	q.mu.Lock()
	empty := len(q.feedback) == 0
	q.mu.Unlock()
	if empty {
		reactionQueuesMu.Lock()
		// Re-check: another goroutine may have enqueued between our drain and this lock.
		q2, ok := reactionQueues[roomID]
		if ok {
			q2.mu.Lock()
			stillEmpty := len(q2.feedback) == 0
			q2.mu.Unlock()
			if stillEmpty {
				delete(reactionQueues, roomID)
			}
		}
		reactionQueuesMu.Unlock()
	}
	return items
}

// FormatReactionFeedback formats reaction feedback as context for the AI.
// Keep the string stable and channel-specific so the model can reason about where the reaction happened.
func FormatReactionFeedback(feedback []ReactionFeedback) string {
	if len(feedback) == 0 {
		return ""
	}

	var lines []string
	for _, f := range feedback {
		ts := f.Timestamp.Format("2006-01-02 15:04:05")
		roomLabel := f.RoomName
		if roomLabel == "" {
			roomLabel = "chat"
		}
		if f.Action == "removed" {
			lines = append(lines, fmt.Sprintf("System: [%s] Matrix reaction removed: %s by %s in %s msg %s", ts, f.Emoji, f.Sender, roomLabel, f.MessageID))
		} else {
			lines = append(lines, fmt.Sprintf("System: [%s] Matrix reaction added: %s by %s in %s msg %s", ts, f.Emoji, f.Sender, roomLabel, f.MessageID))
		}
	}
	return strings.Join(lines, "\n")
}
