package runtime

import "strings"

func NormalizeQueueMode(raw string) (QueueMode, bool) {
	cleaned := strings.TrimSpace(strings.ToLower(raw))
	switch cleaned {
	case "queue", "queued":
		return QueueModeSteer, true
	case "interrupt", "interrupts", "abort":
		return QueueModeInterrupt, true
	case "steer", "steering":
		return QueueModeSteer, true
	case "followup", "follow-ups", "followups":
		return QueueModeFollowup, true
	case "collect", "coalesce":
		return QueueModeCollect, true
	case "steer+backlog", "steer-backlog", "steer_backlog":
		return QueueModeSteerBacklog, true
	case "backlog":
		return QueueModeBacklog, true
	default:
		return "", false
	}
}

func NormalizeQueueDropPolicy(raw string) (QueueDropPolicy, bool) {
	cleaned := strings.TrimSpace(strings.ToLower(raw))
	switch cleaned {
	case "old", "oldest":
		return QueueDropOld, true
	case "new", "newest":
		return QueueDropNew, true
	case "summarize", "summary":
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
