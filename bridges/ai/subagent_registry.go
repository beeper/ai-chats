package ai

import (
	"context"
	"time"

	"maunium.net/go/mautrix/id"
)

type subagentRun struct {
	RunID        string
	ChildRoomID  id.RoomID
	ParentRoomID id.RoomID
	Label        string
	Task         string
	Cleanup      string
	StartedAt    time.Time
	Timeout      time.Duration
}

func (oc *AIClient) listSubagentRunsForParent(parent id.RoomID) []*subagentRun {
	if oc == nil || parent == "" {
		return nil
	}
	oc.subagentRunsMu.Lock()
	defer oc.subagentRunsMu.Unlock()
	var runs []*subagentRun
	for _, run := range oc.subagentRuns {
		if run != nil && run.ParentRoomID == parent {
			runs = append(runs, run)
		}
	}
	return runs
}

func (oc *AIClient) stopSubagentRuns(ctx context.Context, parent id.RoomID) int {
	if oc == nil || parent == "" {
		return 0
	}
	runs := oc.listSubagentRunsForParent(parent)
	stopped := 0
	for _, run := range runs {
		if run == nil || run.ChildRoomID == "" {
			continue
		}
		canceled := oc.cancelRoomRun(run.ChildRoomID)
		drained := oc.drainPendingQueue(run.ChildRoomID)
		finalized := oc.finalizeStoppedQueueItems(ctx, drained)
		if canceled || finalized > 0 {
			stopped++
		}
	}
	return stopped
}

func (oc *AIClient) registerSubagentRun(run *subagentRun) {
	if oc == nil || run == nil || run.RunID == "" {
		return
	}
	oc.subagentRunsMu.Lock()
	defer oc.subagentRunsMu.Unlock()
	if oc.subagentRuns == nil {
		oc.subagentRuns = make(map[string]*subagentRun)
	}
	oc.subagentRuns[run.RunID] = run
}

func (oc *AIClient) unregisterSubagentRun(runID string) {
	if oc == nil || runID == "" {
		return
	}
	oc.subagentRunsMu.Lock()
	defer oc.subagentRunsMu.Unlock()
	delete(oc.subagentRuns, runID)
}
