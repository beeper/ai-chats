package runtime

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
