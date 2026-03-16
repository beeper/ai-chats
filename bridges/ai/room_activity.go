package ai

func (oc *AIClient) hasInflightRequests() bool {
	if oc == nil {
		return false
	}

	oc.activeRoomsMu.Lock()
	active := false
	for _, inFlight := range oc.activeRooms {
		if inFlight {
			active = true
			break
		}
	}
	oc.activeRoomsMu.Unlock()
	if active {
		return true
	}

	oc.pendingQueuesMu.Lock()
	defer oc.pendingQueuesMu.Unlock()
	for _, queue := range oc.pendingQueues {
		if queue != nil && (len(queue.items) > 0 || queue.droppedCount > 0) {
			return true
		}
	}
	return false
}
