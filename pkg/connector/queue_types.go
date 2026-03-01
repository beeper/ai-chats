package connector

import (
	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
)

type QueueMode = airuntime.QueueMode

const (
	QueueModeSteer        QueueMode = airuntime.QueueModeSteer
	QueueModeFollowup     QueueMode = airuntime.QueueModeFollowup
	QueueModeCollect      QueueMode = airuntime.QueueModeCollect
	QueueModeSteerBacklog QueueMode = airuntime.QueueModeSteerBacklog
	QueueModeInterrupt    QueueMode = airuntime.QueueModeInterrupt
)

type QueueDropPolicy = airuntime.QueueDropPolicy

const (
	QueueDropOld       QueueDropPolicy = airuntime.QueueDropOld
	QueueDropNew       QueueDropPolicy = airuntime.QueueDropNew
	QueueDropSummarize QueueDropPolicy = airuntime.QueueDropSummarize
)

const (
	DefaultQueueDebounceMs = airuntime.DefaultQueueDebounceMs
	DefaultQueueCap        = airuntime.DefaultQueueCap
)

const DefaultQueueDrop = airuntime.DefaultQueueDrop
const DefaultQueueMode = airuntime.DefaultQueueMode

type QueueSettings = airuntime.QueueSettings
type QueueInlineOptions = airuntime.QueueInlineOptions

func normalizeQueueMode(raw string) (QueueMode, bool) {
	return airuntime.NormalizeQueueMode(raw)
}

func normalizeQueueDropPolicy(raw string) (QueueDropPolicy, bool) {
	return airuntime.NormalizeQueueDropPolicy(raw)
}
