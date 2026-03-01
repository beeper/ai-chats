package runtime

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
	case QueueModeSteerBacklog:
		return QueueDecision{Action: QueueActionEnqueue, Reason: "steer_backlog_mode"}
	case QueueModeBacklog:
		return QueueDecision{Action: QueueActionEnqueue, Reason: "backlog_mode"}
	default:
		return QueueDecision{Action: QueueActionEnqueue, Reason: "default_backlog"}
	}
}
