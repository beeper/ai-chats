package ai

import (
	"strings"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
)

type queueSummaryState struct {
	DropPolicy   airuntime.QueueDropPolicy
	DroppedCount int
	SummaryLines []string
}

type queueState[T any] struct {
	queueSummaryState
	Items []T
	Cap   int
}

func applyQueueDropPolicy[T any](params struct {
	Queue        *queueState[T]
	Summarize    func(item T) string
	SummaryLimit int
}) bool {
	if params.Queue == nil {
		return false
	}
	if params.Queue.Cap <= 0 || len(params.Queue.Items) < params.Queue.Cap {
		return true
	}
	overflow := airuntime.ResolveQueueOverflow(params.Queue.Cap, len(params.Queue.Items), params.Queue.DropPolicy)
	if !overflow.KeepNew {
		return false
	}
	dropCount := overflow.ItemsToDrop
	if dropCount < 1 {
		return true
	}
	dropped := params.Queue.Items[:dropCount]
	params.Queue.Items = params.Queue.Items[dropCount:]
	if overflow.ShouldSummarize {
		for _, item := range dropped {
			params.Queue.DroppedCount++
			summary := strings.TrimSpace(params.Summarize(item))
			if summary != "" {
				params.Queue.SummaryLines = append(params.Queue.SummaryLines, airuntime.BuildQueueSummaryLine(summary, 160))
			}
		}
		limit := params.SummaryLimit
		if limit <= 0 {
			limit = params.Queue.Cap
		}
		if len(params.Queue.SummaryLines) > limit {
			params.Queue.SummaryLines = params.Queue.SummaryLines[len(params.Queue.SummaryLines)-limit:]
		}
	}
	return true
}
