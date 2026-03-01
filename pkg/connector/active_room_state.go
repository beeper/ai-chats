package connector

import "maunium.net/go/mautrix/id"

func (oc *AIClient) roomHasActiveRun(roomID id.RoomID) bool {
	if oc == nil || roomID == "" {
		return false
	}
	oc.activeRoomsMu.Lock()
	defer oc.activeRoomsMu.Unlock()
	return oc.activeRooms[roomID]
}
