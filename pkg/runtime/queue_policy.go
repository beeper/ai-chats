package runtime

import "strings"

func NormalizeQueueMode(raw string) (QueueMode, bool) {
	cleaned := strings.TrimSpace(strings.ToLower(raw))
	switch cleaned {
	case "interrupt":
		return QueueModeInterrupt, true
	case "steer":
		return QueueModeSteer, true
	case "followup":
		return QueueModeFollowup, true
	case "collect":
		return QueueModeCollect, true
	case "steer+backlog":
		return QueueModeSteerBacklog, true
	default:
		return "", false
	}
}

func NormalizeQueueDropPolicy(raw string) (QueueDropPolicy, bool) {
	cleaned := strings.TrimSpace(strings.ToLower(raw))
	switch cleaned {
	case "old":
		return QueueDropOld, true
	case "new":
		return QueueDropNew, true
	case "summarize":
		return QueueDropSummarize, true
	default:
		return "", false
	}
}

func ResolveQueueBehavior(mode QueueMode) QueueBehavior {
	switch mode {
	case QueueModeSteer:
		return QueueBehavior{Steer: true}
	case QueueModeFollowup:
		return QueueBehavior{Followup: true}
	case QueueModeCollect:
		return QueueBehavior{Followup: true, Collect: true}
	case QueueModeSteerBacklog:
		return QueueBehavior{Steer: true, Followup: true, BacklogAfter: true}
	default:
		return QueueBehavior{}
	}
}

type QueueOverflowResult struct {
	KeepNew         bool
	ItemsToDrop     int
	ShouldSummarize bool
}

func ResolveQueueOverflow(capacity int, currentLen int, policy QueueDropPolicy) QueueOverflowResult {
	if capacity <= 0 || currentLen < capacity {
		return QueueOverflowResult{KeepNew: true}
	}
	if policy == QueueDropNew {
		return QueueOverflowResult{
			KeepNew:         false,
			ItemsToDrop:     0,
			ShouldSummarize: false,
		}
	}
	dropCount := currentLen - capacity + 1
	if dropCount < 1 {
		return QueueOverflowResult{KeepNew: true}
	}
	return QueueOverflowResult{
		KeepNew:         true,
		ItemsToDrop:     dropCount,
		ShouldSummarize: policy == QueueDropSummarize,
	}
}

func DecideQueueAction(mode QueueMode, hasActiveRun bool, isHeartbeat bool) QueueDecision {
	if !hasActiveRun {
		return QueueDecision{Action: QueueActionRunNow, Reason: "no_active_run"}
	}
	if isHeartbeat {
		return QueueDecision{Action: QueueActionEnqueue, Reason: "heartbeat_backlog"}
	}
	switch mode {
	case QueueModeInterrupt:
		return QueueDecision{Action: QueueActionInterruptAndRun, Reason: "interrupt_mode"}
	case QueueModeSteer:
		return QueueDecision{Action: QueueActionEnqueue, Reason: "steer_mode"}
	case QueueModeFollowup:
		return QueueDecision{Action: QueueActionEnqueue, Reason: "followup_mode"}
	case QueueModeCollect:
		return QueueDecision{Action: QueueActionEnqueue, Reason: "collect_mode"}
	case QueueModeSteerBacklog:
		return QueueDecision{Action: QueueActionEnqueue, Reason: "steer_backlog_mode"}
	case QueueModeBacklog:
		return QueueDecision{Action: QueueActionEnqueue, Reason: "backlog_mode"}
	default:
		return QueueDecision{Action: QueueActionEnqueue, Reason: "default_backlog"}
	}
}

// ElideQueueText truncates text to the given character limit with an ellipsis.
func ElideQueueText(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 1 {
		return text[:1]
	}
	return strings.TrimRight(text[:limit-1], " \t\r\n") + "…"
}

// BuildQueueSummaryLine collapses whitespace and elides text to the given limit.
func BuildQueueSummaryLine(text string, limit int) string {
	cleaned := strings.Join(strings.Fields(text), " ")
	return ElideQueueText(cleaned, limit)
}
