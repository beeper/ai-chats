package connector

import (
	"strconv"
	"strings"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
)

type queueSummaryState struct {
	DropPolicy   QueueDropPolicy
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
	overflow := airuntime.ResolveQueueOverflow(params.Queue.Cap, len(params.Queue.Items), airuntime.QueueDropPolicy(params.Queue.DropPolicy))
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
		if limit < 0 {
			limit = 0
		}
		if len(params.Queue.SummaryLines) > limit {
			params.Queue.SummaryLines = params.Queue.SummaryLines[len(params.Queue.SummaryLines)-limit:]
		}
	}
	return true
}

func buildQueueSummaryPrompt(state *pendingQueue, noun string) string {
	if state == nil || state.dropPolicy != airuntime.QueueDropSummarize || state.droppedCount <= 0 {
		return ""
	}
	title := "[Queue overflow] Dropped " + itoa(state.droppedCount) + " " + noun
	if state.droppedCount != 1 {
		title += "s"
	}
	title += " due to cap."
	lines := []string{title}
	if len(state.summaryLines) > 0 {
		lines = append(lines, "Summary:")
		for _, line := range state.summaryLines {
			lines = append(lines, "- "+line)
		}
	}
	state.droppedCount = 0
	state.summaryLines = nil
	return strings.Join(lines, "\n")
}

func buildCollectPrompt(title string, items []pendingQueueItem, summary string) string {
	blocks := []string{title}
	if strings.TrimSpace(summary) != "" {
		blocks = append(blocks, summary)
	}
	for idx, item := range items {
		blocks = append(blocks, strings.TrimSpace("---\nQueued #"+itoa(idx+1)+"\n"+item.prompt))
	}
	return strings.Join(blocks, "\n\n")
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
