package connector

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
)

func formatAbortNotice(stopped int) string {
	if stopped <= 0 {
		return "Agent was aborted."
	}
	label := "sub-agents"
	if stopped == 1 {
		label = "sub-agent"
	}
	return fmt.Sprintf("Agent was aborted. Stopped %d %s.", stopped, label)
}

func (oc *AIClient) abortRoom(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) int {
	if portal == nil {
		return 0
	}
	oc.cancelRoomRun(portal.MXID)
	oc.clearPendingQueue(portal.MXID)
	stopped := oc.stopSubagentRuns(portal.MXID)
	if meta != nil {
		meta.AbortedLastRun = true
		oc.savePortalQuiet(ctx, portal, "abort")
	}
	return stopped
}
